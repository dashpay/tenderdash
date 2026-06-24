package evidence

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/service"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

var _ service.Service = (*Reactor)(nil)

const (
	EvidenceChannel = p2p.ChannelID(0x38)

	maxMsgSize = 1048576 // 1MB TODO make it configurable
)

// evidenceSyncInterval controls how often the per-peer sync goroutine re-reads
// the pending pool and re-sends pending evidence to the peer.
//
// Resend profile: each tick sends the pending evidence that fits within
// ConsensusParams.Evidence.MaxBytes — the same budget a proposer applies when
// selecting evidence for a block — to every connected peer, typically
// single-digit items in production. The receiver deduplicates silently via
// isPending/isCommitted guards — no ACK is required. Re-sending continues until
// evidence leaves the pending set (committed or expired) or the peer
// disconnects.
//
// Rationale for re-sending: syncEvidence is the only push path for evidence
// that already exists in the pool when a peer connects. The first batch can be
// silently dropped if PeerStatusUp fires before the p2p channel route is fully
// wired. Without a retry the peer would permanently miss those items.
//
// Interval choice — 1 s:
//
//   - Recovery speed: the routing race resolves in milliseconds; one retry is
//     sufficient, and a 1 s retry window is still well within the expected
//     connection stabilization time.
//
//   - Amplification: each tick sends at most P × M messages (P peers, M pending
//     evidence items). At the default 1 s interval the worst-case send rate is
//     P × M messages/s. Evidence pools in production are tiny (typically ≤ 5
//     items, each << 1 KB), so the overhead is negligible. A larger interval
//     (e.g. 10 s) reduces send frequency but causes an unacceptable regression
//     in tests where goroutines start with an empty pool and must wait the full
//     interval before delivering evidence added after connection.
//
// Declared as a var (not const) so tests can override it via
// SetEvidenceSyncIntervalForTesting without affecting production code.
var evidenceSyncInterval = 1 * time.Second

// peerSyncState holds the cancel function and a unique identity for the
// per-peer syncEvidence goroutine.
//
// The identity solves the Down→Up flap race: when PeerStatusDown fires, the
// handler deletes the map entry synchronously and calls cancel(). The goroutine
// keeps running until it observes the cancellation, and its deferred cleanup
// compares its own id against whatever is in the map at that point — if a fast
// Up event already installed a new goroutine, the ids differ and the stale
// goroutine leaves the new entry untouched.
//
// id is drawn from Reactor.nextSyncID, a monotonic counter incremented under
// the reactor mutex on every install (see processPeerUpdate). Each goroutine
// therefore receives a strictly increasing, never-reused value, so two
// goroutines installed across a flap are guaranteed distinct. A plain counter
// is spoof-proof and self-documenting: unlike a heap-pointer token its
// distinctness does not hinge on the allocator handing back unequal addresses,
// so it cannot be silently defeated by a future refactor (e.g. switching the
// token to *struct{}, whose allocations all share runtime.zerobase).
type peerSyncState struct {
	cancel context.CancelFunc
	id     uint64 // unique per goroutine; sourced from Reactor.nextSyncID
}

// GetChannelDescriptor produces an instance of a descriptor for this
// package's required channels.
func GetChannelDescriptor() *p2p.ChannelDescriptor {
	return &p2p.ChannelDescriptor{
		ID:                  EvidenceChannel,
		Priority:            3,
		RecvMessageCapacity: maxMsgSize,
		RecvBufferCapacity:  32,
		Name:                "evidence",
	}
}

// Reactor handles evpool evidence broadcasting amongst peers.
type Reactor struct {
	service.BaseService
	logger log.Logger

	evpool     *Pool
	chCreator  p2p.ChannelCreator
	evidenceCh p2p.Channel

	peerEvents p2p.PeerEventSubscriber

	mtx sync.Mutex

	// nextSyncID assigns each per-peer syncEvidence goroutine a unique,
	// monotonically increasing identity. Mutated only while holding mtx.
	nextSyncID uint64

	peerRoutines map[types.NodeID]peerSyncState
}

// NewReactor returns a reference to a new evidence reactor, which implements the
// service.Service interface. It accepts a p2p Channel dedicated for handling
// envelopes with EvidenceList messages.
func NewReactor(
	logger log.Logger,
	chCreator p2p.ChannelCreator,
	peerEvents p2p.PeerEventSubscriber,
	evpool *Pool,
) *Reactor {
	r := &Reactor{
		logger:       logger,
		evpool:       evpool,
		chCreator:    chCreator,
		peerEvents:   peerEvents,
		peerRoutines: make(map[types.NodeID]peerSyncState),
	}

	r.BaseService = *service.NewBaseService(logger, "Evidence", r)

	return r
}

// OnStart starts separate go routines for each p2p Channel and listens for
// envelopes on each. In addition, it also listens for peer updates and handles
// messages on that p2p channel accordingly. The caller must be sure to execute
// OnStop to ensure the outbound p2p Channels are closed. No error is returned.
func (r *Reactor) OnStart(ctx context.Context) (err error) {
	r.evidenceCh, err = r.chCreator(ctx, GetChannelDescriptor())
	if err != nil {
		return err
	}

	go r.processEvidenceCh(ctx)
	go r.processPeerUpdates(ctx, r.peerEvents(ctx, "evidence"))

	return nil
}

// OnStop stops the reactor by signaling to all spawned goroutines to exit and
// blocking until they all exit.
func (r *Reactor) OnStop() { r.evpool.Close() }

// handleEvidenceMessage handles envelopes sent from peers on the EvidenceChannel.
// It returns an error only if the Envelope.Message is unknown for this channel
// or if the given evidence is invalid. This should never be called outside of
// handleMessage.
func (r *Reactor) handleEvidenceMessage(ctx context.Context, envelope *p2p.Envelope) error {
	logger := r.logger.With("peer", envelope.From)

	switch msg := envelope.Message.(type) {
	case *tmproto.Evidence:

		// Only accept evidence if we are an active validator.
		// On other hosts, signatures in evidence (if any) cannot be verified due to lack of validator public keys,
		// and it creates risk of adding invalid evidence to the pool.
		//
		// TODO: We need to figure out how to handle evidence from non-validator nodes, to avoid scenarios where some
		// evidence is lost.
		if !r.evpool.hasPublicKeys() {
			// silently drop the message
			logger.Debug("dropping evidence message as we don't have validator public keys", "evidence", envelope.Message)
			return nil
		}

		// Process the evidence received from a peer
		// Evidence is sent and received one by one
		ev, err := types.EvidenceFromProto(msg)
		if err != nil {
			logger.Error("failed to convert evidence", "error", err)
			return err
		}

		// If the evidence is already pending or committed, we don't need to
		// broadcast it again.
		if !r.evpool.isPending(ev) && !r.evpool.isCommitted(ev) {
			if err := r.evpool.AddEvidence(ctx, ev); err != nil {
				// If we're given invalid evidence by the peer, notify the router that
				// we should remove this peer by returning an error.
				if _, ok := err.(*types.ErrInvalidEvidence); ok {
					return err
				}
				// Any other error means we could not add the evidence to the pool —
				// most commonly because we are behind and lack the block needed to
				// verify it (verify() returns a plain error, NOT ErrInvalidEvidence,
				// for a missing block so the peer is not disconnected). The evidence
				// never entered the pending set, so isPending stays false. Falling
				// through to broadcastEvidence here would re-gossip on every receipt
				// without ever converging — sustained amplification across the
				// network. Log and stop; only successfully-added evidence is broadcast.
				logger.Error("failed to add evidence", "error", err)
				return nil
			}

			return r.broadcastEvidence(ctx, *msg, r.evidenceCh)
		}
		logger.Debug("evidence already pending", "evidence", ev)
		return nil

	default:
		return fmt.Errorf("received unknown message: %T", msg)
	}
}

// handleMessage handles an Envelope sent from a peer on a specific p2p Channel.
// It will handle errors and any possible panics gracefully. A caller can handle
// any error returned by sending a PeerError on the respective channel.
func (r *Reactor) handleMessage(ctx context.Context, envelope *p2p.Envelope) (err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("panic in processing message: %v", e)
			r.logger.Error(
				"recovering from processing message panic",
				"err", err,
				"stack", string(debug.Stack()),
			)
		}
	}()

	// r.logger.Debug("received message", "message", envelope.Message, "peer", envelope.From)

	switch envelope.ChannelID {
	case EvidenceChannel:
		err = r.handleEvidenceMessage(ctx, envelope)
	default:
		err = fmt.Errorf("unknown channel ID (%d) for envelope (%v)", envelope.ChannelID, envelope)
	}

	return
}

// processEvidenceCh implements a blocking event loop where we listen for p2p
// Envelope messages from the evidenceCh.
func (r *Reactor) processEvidenceCh(ctx context.Context) {
	iter := r.evidenceCh.Receive(ctx)
	for iter.Next(ctx) {
		envelope := iter.Envelope()
		if err := r.handleMessage(ctx, envelope); err != nil {
			r.logger.Error("failed to process message", "ch_id", envelope.ChannelID, "envelope", envelope, "err", err)
			if serr := r.evidenceCh.SendError(ctx, p2p.PeerError{
				NodeID: envelope.From,
				Err:    err,
			}); serr != nil {
				return
			}
		}
	}
}

// processPeerUpdate processes a PeerUpdate. For new or live peers it will check
// if an evidence broadcasting goroutine needs to be started. For down or
// removed peers, it will check if an evidence broadcasting goroutine
// exists and signal that it should exit.
//
// Concurrency note: processPeerUpdates drives this function from a single
// goroutine, so Up and Down events are always sequential. However, the
// syncEvidence goroutine runs concurrently and its deferred map-cleanup can
// race with a fast Down→Up reconnect (the goroutine may not have run its defer
// by the time Up fires). This is handled by:
//   - PeerStatusDown: cancel the goroutine AND delete the map entry
//     synchronously, so the subsequent Up always starts fresh.
//   - syncEvidence defer: compares its own monotonic identity (id) before
//     deleting, so it cannot accidentally remove a new goroutine's entry.
//
// FIXME: The peer may be behind in which case it would simply ignore the
// evidence and treat it as invalid. This would cause the peer to disconnect.
// The peer may also receive the same piece of evidence multiple times if it
// connects/disconnects frequently from the broadcasting peer(s).
//
// REF: https://github.com/tendermint/tendermint/issues/4727
func (r *Reactor) processPeerUpdate(ctx context.Context, peerUpdate p2p.PeerUpdate) {
	r.logger.Trace("received peer update", "peer", peerUpdate.NodeID, "status", peerUpdate.Status)

	r.mtx.Lock()
	defer r.mtx.Unlock()

	switch peerUpdate.Status {
	case p2p.PeerStatusUp:
		// Do not allow starting new evidence broadcast loops after reactor shutdown
		// has been initiated. This can happen after we've manually closed all
		// peer broadcast loops, but the router still sends in-flight peer updates.
		if !r.IsRunning() {
			return
		}

		// Check if we've already started a goroutine for this peer, if not we create
		// a new done channel so we can explicitly close the goroutine if the peer
		// is later removed, we increment the waitgroup so the reactor can stop
		// safely, and finally start the goroutine to broadcast evidence to that peer.
		if _, ok := r.peerRoutines[peerUpdate.NodeID]; !ok {
			pctx, pcancel := context.WithCancel(ctx)
			// Assign a fresh identity under the mutex so installs racing across a
			// Down→Up flap can never collide.
			r.nextSyncID++
			entry := peerSyncState{cancel: pcancel, id: r.nextSyncID}
			r.peerRoutines[peerUpdate.NodeID] = entry
			go r.syncEvidence(pctx, peerUpdate.NodeID, entry.id)
		}

	case p2p.PeerStatusDown:
		// Cancel the goroutine and delete the map entry synchronously.
		// Deleting here (rather than leaving it for the goroutine's deferred
		// cleanup) ensures that a fast Down→Up reconnect — arriving before the
		// goroutine's defer fires — always starts a new sync goroutine instead
		// of seeing the stale entry and skipping.
		if entry, ok := r.peerRoutines[peerUpdate.NodeID]; ok {
			entry.cancel()
			delete(r.peerRoutines, peerUpdate.NodeID)
		}
	}
}

// processPeerUpdates initiates a blocking process where we listen for and handle
// PeerUpdate messages. When the reactor is stopped, we will catch the signal and
// close the p2p PeerUpdatesCh gracefully.
func (r *Reactor) processPeerUpdates(ctx context.Context, peerUpdates *p2p.PeerUpdates) {
	for {
		select {
		case peerUpdate := <-peerUpdates.Updates():
			r.processPeerUpdate(ctx, peerUpdate)
		case <-ctx.Done():
			return
		}
	}
}

// syncEvidence periodically sends pending pool evidence to a newly connected
// peer until the peer disconnects (ctx is canceled). It is invoked in a
// goroutine per unique peer ID when a PeerStatusUp event is received.
//
// The goroutine re-syncs on every evidenceSyncInterval tick so that evidence
// dropped during the initial delivery (when PeerStatusUp fires before the p2p
// channel route to the peer is fully wired) is recovered automatically.
// Without the retry loop, evidence created before the peer connected would be
// permanently missed — syncEvidence is the only push path for such evidence.
//
// Note: a peer may receive the same evidence more than once; this is handled
// gracefully by the receiver (isPending / isCommitted guards in
// handleEvidenceMessage).
//
// myID is this goroutine's unique identity, drawn from Reactor.nextSyncID, so it
// differs from every other goroutine's id (see peerSyncState). The deferred
// cleanup compares it against the current map entry to avoid removing a newer
// entry installed by a fast Down→Up peer reconnect.
func (r *Reactor) syncEvidence(ctx context.Context, peerID types.NodeID, myID uint64) {
	defer func() {
		r.mtx.Lock()
		// Only remove OUR map entry. PeerStatusDown already deletes it
		// synchronously, and a fast Down→Up may have installed a new goroutine's
		// entry by the time this defer runs — in both cases the IDs differ and
		// we must leave the map untouched.
		if current, ok := r.peerRoutines[peerID]; ok && current.id == myID {
			delete(r.peerRoutines, peerID)
		}
		r.mtx.Unlock()

		if e := recover(); e != nil {
			r.logger.Error(
				"recovering from broadcasting evidence loop",
				"err", e,
				"stack", string(debug.Stack()),
			)
		}
	}()

	ticker := time.NewTicker(evidenceSyncInterval)
	defer ticker.Stop()

	for {
		// Send pending evidence to the peer, bounded by the same byte budget a
		// proposer applies when selecting evidence for a block
		// (ConsensusParams.Evidence.MaxBytes). Reusing PendingEvidence keeps this
		// push symmetric with proposal selection and avoids re-walking the whole
		// pool every tick (O(N²·M) gossip on a full mesh). The peer may be behind
		// and unable to process some items, and may receive duplicates across
		// ticks — both are handled idempotently on the remote side.
		maxBytes := r.evpool.State().ConsensusParams.Evidence.MaxBytes
		evList, _ := r.evpool.PendingEvidence(maxBytes)
		for _, ev := range evList {
			evProto, err := types.EvidenceToProto(ev)
			if err != nil {
				panic(fmt.Errorf("failed to convert evidence: %w", err))
			}

			if err := r.evidenceCh.Send(ctx, p2p.Envelope{
				To:      peerID,
				Message: evProto,
			}); err != nil {
				return
			}
			r.logger.Trace("evidence sync: sent evidence to peer", "evidence", ev, "peer", peerID)

			select {
			case <-ctx.Done():
				return
			default:
			}
		}
		r.logger.Trace("evidence sync finished", "peer", peerID)

		// Wait for the next sync cycle or peer disconnect.
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// broadcastEvidence sends new evidence to all connected peers.
func (r *Reactor) broadcastEvidence(ctx context.Context, evidence tmproto.Evidence, evidenceCh p2p.Channel) error {

	if err := evidenceCh.Send(ctx, p2p.Envelope{
		Broadcast: true,
		Message:   &evidence,
	}); err != nil {
		return fmt.Errorf("failed to broadcast evidence: %w", err)
	}
	r.logger.Debug("evidence broadcasted", "evidence", evidence)

	return nil
}
