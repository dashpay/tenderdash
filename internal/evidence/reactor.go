package evidence

import (
	"context"
	"fmt"
	"runtime/debug"

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

	peerRoutines map[types.NodeID]context.CancelFunc
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
		peerRoutines: make(map[types.NodeID]context.CancelFunc),
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
				logger.Error("failed to add evidence", "error", err)
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
		_, ok := r.peerRoutines[peerUpdate.NodeID]
		if !ok {
			pctx, pcancel := context.WithCancel(ctx)
			r.peerRoutines[peerUpdate.NodeID] = pcancel
			go r.syncEvidence(pctx, peerUpdate.NodeID)
		}

	case p2p.PeerStatusDown:
		// Check if we've started an evidence broadcasting goroutine for this peer.
		// If we have, we signal to terminate the goroutine via the channel's closure.
		// This will internally decrement the peer waitgroup and remove the peer
		// from the map of peer evidence broadcasting goroutines.
		closer, ok := r.peerRoutines[peerUpdate.NodeID]
		if ok {
			closer()
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

// syncEvidence starts a blocking process that sends all evidence to a newly
// connected peer.  This should be invoked in a goroutine per unique peer
// ID via an appropriate PeerUpdate. The goroutine can be signaled to gracefully
// exit by either explicitly closing the provided doneCh or by the reactor
// signaling to stop.
func (r *Reactor) syncEvidence(ctx context.Context, peerID types.NodeID) {
	defer func() {
		r.mtx.Lock()
		delete(r.peerRoutines, peerID)
		r.mtx.Unlock()

		if e := recover(); e != nil {
			r.logger.Error(
				"recovering from broadcasting evidence loop",
				"err", e,
				"stack", string(debug.Stack()),
			)
		}
	}()

	next := r.evpool.EvidenceFront()

	for next != nil {
		ev := next.Value.(types.Evidence)
		evProto, err := types.EvidenceToProto(ev)
		if err != nil {
			panic(fmt.Errorf("failed to convert evidence: %w", err))
		}

		// Send the evidence to the corresponding peer. Note, the peer may be behind
		// and thus would not be able to process the evidence correctly. Also, the
		// peer may receive this piece of evidence multiple times if it added and
		// removed frequently from the broadcasting peer.

		if err := r.evidenceCh.Send(ctx, p2p.Envelope{
			To:      peerID,
			Message: evProto,
		}); err != nil {
			return
		}
		r.logger.Debug("evidence sync: sent evidence to peer", "evidence", ev, "peer", peerID)

		select {
		case <-ctx.Done():
			return
		default:
		}

		next = next.Next()
	}
	r.logger.Debug("evidence sync finished", "peer", peerID)
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
