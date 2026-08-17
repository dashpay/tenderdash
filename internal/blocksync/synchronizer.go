package blocksync

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jonboulle/clockwork"
	sync "github.com/sasha-s/go-deadlock"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/client"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/service"
	"github.com/dashpay/tenderdash/libs/workerpool"
	bcproto "github.com/dashpay/tenderdash/proto/tendermint/blocksync"
	"github.com/dashpay/tenderdash/types"
)

/*
eg, L = latency = 0.1s
	P = num peers = 10
	FN = num full nodes
	BS = 1kB block size
	CB = 1 Mbit/s = 128 kB/s
	CB/P = 12.8 kB
	B/S = CB/P/BS = 12.8 blocks/s

	12.8 * 0.1 = 1.28 blocks on conn
*/

const (
	requestInterval                     = 2 * time.Millisecond
	poolWorkerSize                      = 600
	maxPendingRequestsPerPeer           = 20
	defaultSyncRateIntervalBlocks int64 = 100

	// maxPendingApplyBytes is how much block data block sync may hold waiting for
	// a lower height to arrive, summed from the serialized size recorded while
	// each response was decoded. It is the limit that decides what the backlog
	// costs once blocks are large, because a count of blocks says nothing about
	// that when one block may be tens of megabytes.
	//
	// Serialized bytes understate the heap actually retained. The size covers the
	// block alone, not the commit held alongside it, and a decoded block costs
	// more than its wire form - several times more for blocks small enough that
	// fixed per-response overhead dominates. Read this as a budget for block
	// data, not as the memory the backlog occupies.
	maxPendingApplyBytes = 256 << 20

	// maxOutstandingHeights is how many heights may be requested and not yet
	// applied. It is the second half of the bound, not a restatement of it: the
	// byte budget can only be spent by a response that has already arrived, so a
	// height still in flight costs it nothing. Without a limit on those, an
	// empty backlog would let the producer run as far ahead as the peers and the
	// worker pool allow, and every one of those requests would land in the
	// backlog the moment a height below them went missing.
	//
	// Which of the two binds depends on block size, and they cross at
	// maxPendingApplyBytes/maxOutstandingHeights, 4 MiB a block. Below that the
	// count binds and the budget is never approached - sixty-four 2 KiB blocks
	// are 128 KiB of block data - so the common case is bounded by this limit
	// and not by the budget at all. Above it the budget binds first and stops
	// the backlog at 256 MiB however few blocks that is. Neither limit can see
	// the case the other covers.
	//
	// What the two bound together is
	//
	//	held <= maxPendingApplyBytes + maxOutstandingHeights * largest block accepted
	//
	// The last term is not the chain's configured maximum. The block sync
	// channel accepts a message up to types.MaxBlockSizeBytes, 100 MiB,
	// whatever the chain's own parameters say, and a block is not rejected for
	// exceeding those parameters until it is applied - which is precisely what
	// does not happen while a lower height is missing. The ceiling is therefore
	// unconditional, about 6.98 GB, and configuring a chain for smaller blocks
	// does not lower it.
	//
	// Lowering maxOutstandingHeights does, in proportion, and it is the only
	// lever on it here. 64 is the balance struck: holding that term to the
	// budget itself would mean a window of two, and even a 1 GiB allowance for
	// it gives ten, either of which throttles every chain whose peers never
	// actually send a block near the ingress limit. At a tenth of a second per
	// round trip, 64 in flight fetch several hundred blocks a second, far more
	// than applying them can consume.
	//
	// The second term is the fetch pipeline's footprint rather than the
	// backlog's, and it is not the only one of its kind: the block sync
	// channel's receive queue is sized from a RecvBufferCapacity of 1024, which
	// the default simple-priority queue squares into a limit of 1,048,576
	// messages, each able to carry a whole block. That is outside what these
	// limits cover.
	maxOutstandingHeights = 64

	// maxConsecutiveFailures is how many block requests in a row a peer may fail
	// before we drop it. Requests time out under load, and a peer serves its
	// requests one at a time, so a single failure says very little about the
	// peer. Dropping one costs the rest of its in-flight requests too.
	maxConsecutiveFailures int32 = 5

	// Minimum recv rate to ensure we're receiving blocks from a peer fast
	// enough. If a peer is not sending us data at at least that rate, we
	// consider them to have timed out and we disconnect.
	//
	// Assuming a DSL connection (not a good choice) 128 Kbps (upload) ~ 15 KB/s,
	// sending data across atlantic ~ 7.5 KB/s.
	minRecvRate = 7680
)

// errDuplicateBlock reports a block response for a height that is already pending
// or already applied. Requesting a height twice causes it, so the peer that
// answered is not at fault and must not be reported to the p2p layer.
var errDuplicateBlock = errors.New("block response already exists")

/*
	Peers self report their heights when we join the block synchronizer.
	Starting from our latest synchronizer.height, we request blocks
	in sequence from peers that reported higher heights than ours.
	Every so often we ask peers what height they're on so we can keep going.

	Requests are continuously made for blocks of higher heights until
	the limit is reached. If most of the requests have no available peers, and we
	are not at peer limits, we can probably switch to consensus reactor
*/

type (
	PeerAdder interface {
		AddPeer(peer PeerData)
	}
	PeerRemover interface {
		RemovePeer(peerID types.NodeID)
	}

	// Synchronizer keeps track of the block sync peers, block requests and block responses.
	Synchronizer struct {
		service.BaseService
		logger log.Logger

		lastAdvance time.Time

		mtx sync.RWMutex

		height int64 // the lowest key in requesters.

		clock clockwork.Clock

		// atomic
		jobProgressCounter atomic.Int32 // number of requests pending assignment or block response

		startHeight       int64
		monitorInterval   int64
		lastMonitorUpdate time.Time
		lastSyncRate      float64

		peerStore      *InMemPeerStore
		client         client.BlockClient
		applier        *blockApplier
		workerPool     *workerpool.WorkerPool
		jobGen         *jobGenerator
		pendingToApply map[int64]BlockResponse

		// ctx/cancel scope the handler goroutines' lifetime. Created in OnStart
		// (live before the goroutines spawn, so it never observes a start-race)
		// and canceled in OnStop so Stop releases the handlers even when the
		// caller's context is still live.
		ctx    context.Context
		cancel context.CancelFunc
	}
	OptionFunc func(v *Synchronizer)
)

func WithWorkerPool(wp *workerpool.WorkerPool) OptionFunc {
	return func(v *Synchronizer) {
		v.workerPool = wp
	}
}

func WithLogger(logger log.Logger) OptionFunc {
	return func(v *Synchronizer) {
		v.logger = logger
	}
}

func WithClock(clock clockwork.Clock) OptionFunc {
	return func(v *Synchronizer) {
		v.clock = clock
	}
}

func WithMonitorInterval(blocks int64) OptionFunc {
	return func(v *Synchronizer) {
		if blocks > 0 {
			v.monitorInterval = blocks
		}
	}
}

// NewSynchronizer returns a new Synchronizer with the height equal to start
func NewSynchronizer(start int64, client client.BlockClient, blockExec *blockApplier, opts ...OptionFunc) *Synchronizer {
	peerStore := NewInMemPeerStore()
	logger := log.NewNopLogger()
	bp := &Synchronizer{
		logger:          logger,
		clock:           clockwork.NewRealClock(),
		client:          client,
		applier:         blockExec,
		peerStore:       peerStore,
		jobGen:          newJobGenerator(start, logger, client, peerStore),
		startHeight:     start,
		height:          start,
		monitorInterval: defaultSyncRateIntervalBlocks,
		workerPool:      workerpool.New(poolWorkerSize, workerpool.WithLogger(logger)),
		pendingToApply:  map[int64]BlockResponse{},
	}
	for _, opt := range opts {
		opt(bp)
	}
	bp.BaseService = *service.NewBaseService(logger, "Synchronizer", bp)
	return bp
}

// OnStart implements service.Service by spawning requesters routine and recording
// synchronizer's start time.
func (s *Synchronizer) OnStart(ctx context.Context) error {
	if s.monitorInterval <= 0 {
		s.monitorInterval = defaultSyncRateIntervalBlocks
	}
	s.lastAdvance = s.clock.Now()
	s.lastMonitorUpdate = s.lastAdvance
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.workerPool.Run(s.ctx)
	go s.runHandler(s.ctx, s.produceJob)
	go s.runHandler(s.ctx, s.consumeJobResult)
	return nil
}

func (s *Synchronizer) OnStop() {
	s.cancel()
	s.workerPool.Stop(context.Background())
}

func (s *Synchronizer) produceJob(ctx context.Context) error {
	if !s.jobGen.shouldJobBeGenerated(s.backlog()) {
		// TODO need to come up with a smarter way how to produce jobs without sleeping
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.clock.After(50 * time.Millisecond):
		}
		return nil
	}
	// remove timed out peers and redo its heights again
	s.removeTimedoutPeers(ctx)
	// Count the job as in-progress before Send: a worker may run it and
	// consumeJobResult may decrement before Send even returns, so incrementing
	// afterwards could drive the counter transiently negative. Every path that
	// fails to hand the job to a worker must undo this increment, otherwise the
	// in-progress count reported by GetStatus leaks upward permanently.
	s.jobProgressCounter.Add(1)
	job, err := s.jobGen.nextJob(ctx)
	if err != nil {
		s.jobProgressCounter.Add(-1)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		s.logger.Error("cannot create a next job", "error", err)
		return nil
	}
	err = s.workerPool.Send(ctx, job)
	if err != nil {
		s.jobProgressCounter.Add(-1)
		if errors.Is(err, workerpool.ErrWorkerPoolStopped) ||
			errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		s.logger.Error("cannot add a job to worker-pool", "error", err)
	}
	return nil
}

func (s *Synchronizer) consumeJobResult(ctx context.Context) error {
	res, err := s.workerPool.Receive(ctx)
	if err != nil {
		if errors.Is(err, workerpool.ErrWorkerPoolStopped) ||
			errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		s.logger.Error("cannot receive a job result from worker pool", "error", err)
		return nil
	}
	s.jobProgressCounter.Add(-1)
	if res.Err != nil {
		var bfErr *errBlockFetch
		if !errors.As(res.Err, &bfErr) {
			return nil
		}
		s.jobGen.pushBack(bfErr.height)
		// One failed request is usually a timeout under load, not a bad peer.
		// Dropping the peer also fails its other in-flight requests, each of
		// which drops another peer in turn, so react only to a sustained run of
		// failures.
		if !s.peerStore.AddFailure(bfErr.peerID, maxConsecutiveFailures) {
			s.logger.Debug("block request failed, keeping peer",
				"peer", bfErr.peerID,
				"height", bfErr.height,
				"error", bfErr.err)
			return nil
		}
		s.logger.Error("removing peer after too many consecutive failed block requests",
			"peer", bfErr.peerID,
			"height", bfErr.height,
			"failures", maxConsecutiveFailures,
			"error", bfErr.err)
		s.RemovePeer(bfErr.peerID)
		_ = s.client.Send(ctx, p2p.PeerError{NodeID: bfErr.peerID, Err: bfErr})
		return nil
	}
	resp := res.Value.(*BlockResponse)
	s.peerStore.Update(resp.PeerID, AddNumPending(-1), ResetFailures(), UpdateMonitor(resp.Size))
	err = s.addBlock(*resp)
	if err != nil {
		if !errors.Is(err, errDuplicateBlock) {
			s.logger.Error("cannot add a block to the pending list",
				"height", resp.Block.Height,
				"error", err.Error())
			_ = s.client.Send(ctx, p2p.PeerError{NodeID: resp.PeerID, Err: err})
			return nil
		}
		// A duplicate is not the sending peer's fault, we asked more than one peer for
		// this height. Info level: a healthy sync produces none, so this is the only
		// signal an operator gets that heights are being re-requested.
		s.logger.Info("dropping duplicate block response",
			"height", resp.Block.Height,
			"peer", resp.PeerID,
			"reason", err.Error())
	}
	failed, err := s.applyBlock(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			// Cancellation is our own doing, so no peer is at fault. The response stays
			// pending and is retried when the next result arrives.
			return err
		}
		// Charge the failure to the peer that supplied the failing block: removing it
		// also drops its pending entry, so the height is fetched from someone else.
		s.logger.Error("cannot apply a block",
			"height", failed.Block.Height,
			"peer", failed.PeerID,
			"error", err.Error())
		s.RemovePeer(failed.PeerID)
		_ = s.client.Send(ctx, p2p.PeerError{NodeID: failed.PeerID, Err: err})
	}
	return nil
}

// backlog reports what block sync is holding: the height it is waiting to
// apply, which is the lowest height not applied yet, and the serialized size of
// the responses held above it. That size is what the blocks measured on the
// wire, which is less than what holding them decoded costs; see
// maxPendingApplyBytes. Both are read under one lock, so the pair describes a
// single state and a job cannot be admitted against a height from one moment
// and a backlog from another.
//
// The size is summed on read rather than carried as a counter. pendingToApply
// holds at most maxOutstandingHeights entries, so the sum costs less than the
// two full peer scans the producing loop already runs for every job, and a
// counter would have to be kept correct by addBlock, advance and dropPeer
// independently - which is how an aggregate comes to disagree with what it
// aggregates.
func (s *Synchronizer) backlog() (applyHeight int64, pendingBytes int) {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	for _, resp := range s.pendingToApply {
		pendingBytes += resp.Size
	}
	return s.height, pendingBytes
}

// GetStatus returns synchronizer's height, count of in progress requests
func (s *Synchronizer) GetStatus() (int64, int32) {
	cnt := s.jobProgressCounter.Load()
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	return s.height, cnt
}

// IsCaughtUp returns true if this node is caught up, false - otherwise.
func (s *Synchronizer) IsCaughtUp() bool {
	// Need at least 1 peer to be considered caught up.
	if s.peerStore.IsZero() {
		return false
	}
	maxHeight := s.peerStore.MaxHeight()
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	return s.height >= maxHeight
}

// WaitForSync blocks until block sync is finished, and reports whether the node
// actually caught up.
//
// A stall on its own is not a reason to stop. Handing over to consensus is a
// one-way door - only the state sync path ever switches back to block sync - and
// consensus catch-up is far slower than block sync, so a node that gives up
// while it is still thousands of blocks behind stays behind. As long as some
// peer holds the block we are waiting for there is something to retry, so keep
// going and say so loudly. Give up on the stall only once no peer can serve that
// block, or once the stall has outlasted maxSyncStall, so that a wedged
// synchronizer can still hand over rather than blocking forever.
func (s *Synchronizer) WaitForSync(ctx context.Context) (caughtUp bool) {
	ticker := s.clock.NewTicker(switchToConsensusIntervalSeconds * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return s.IsCaughtUp()
		case <-ticker.Chan():
			if s.IsCaughtUp() {
				return true
			}
			height, stalledFor, servable := s.stallSnapshot()
			// read separately because nothing is decided on it: it only gives the
			// log lines below the number an operator compares against
			maxPeerHeight := s.MaxPeerHeight()
			switch stallVerdictFor(servable, stalledFor) {
			case stopNothingToFetch:
				if maxPeerHeight > height {
					// Peers claim to be ahead yet none of them holds the block we
					// need. Worth saying: the node stops while apparently behind,
					// which otherwise looks like it gave up for no reason.
					s.logger.Error(
						"no peer can serve the next block, handing over to consensus",
						"height", height,
						"max_peer_height", maxPeerHeight,
						"stalled_for", stalledFor,
					)
				}
				return false
			case stopStalledTooLong:
				s.logger.Error(
					"block sync stalled for too long, handing over to consensus while still behind",
					"height", height,
					"max_peer_height", maxPeerHeight,
					"stalled_for", stalledFor,
				)
				return false
			}
			if stalledFor > syncTimeout {
				s.logger.Error(
					"block sync has stalled but a peer still holds the block we need, still trying",
					"height", height,
					"max_peer_height", maxPeerHeight,
					"stalled_for", stalledFor,
					"giving_up_in", maxSyncStall-stalledFor,
				)
				continue
			}
			s.logger.Info(
				"not caught up yet",
				"height", height,
				"max_peer_height", maxPeerHeight,
				"timeout_in", syncTimeout-stalledFor,
			)
		}
	}
}

// stallSnapshot reads everything the stall verdict is formed from as one
// observation: the height block sync is waiting for, how long it has been
// waiting for it, and whether any peer can serve it.
//
// Blocks are applied in order, so the current height is the only one that can
// move us forward, and whether a peer can serve that one is what decides
// whether waiting is worth anything. The highest height anyone claims decides
// nothing: a peer whose blocks start above us has nothing we can use however
// high it claims to be.
//
// The three are read under one lock because advance() stamps the height and the
// advance time together under that same lock. A block applied concurrently
// therefore lands either wholly inside the snapshot or wholly outside it, and
// the verdict can never pair a height with a staleness or a servability
// measured against a different one. Read separately, it could: a height read
// before an advance, checked for servability after one, reports nothing to
// fetch while the height we had by then moved on to is served. Ending block
// sync is a one-way door, so a stop assembled from two inconsistent readings
// leaves the node in consensus catch-up it cannot leave.
func (s *Synchronizer) stallSnapshot() (height int64, stalledFor time.Duration, servable bool) {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	return s.height, s.clock.Since(s.lastAdvance), s.peerStore.HasPeerForHeight(s.height)
}

// stallVerdict says what a lack of progress in block sync means.
type stallVerdict int

const (
	// keepSyncing means the stall is not a reason to stop yet
	keepSyncing stallVerdict = iota
	// stopNothingToFetch means no peer holds the block we are waiting for
	stopNothingToFetch
	// stopStalledTooLong means a peer still holds it but we have to hand over anyway
	stopStalledTooLong
)

// stallVerdictFor decides what to do when block sync has made no progress for
// stalledFor, given whether any peer holds the block we are waiting for.
//
// Waiting on a block someone has is a reason to keep retrying, not to stop:
// handing over to consensus is effectively irreversible, so stopping while
// behind leaves the node grinding through consensus catch-up instead. Only a
// stall on a block nobody has, or one long enough to look like a wedge, ends
// block sync.
//
// servable is judged from what peers advertise about themselves, so it cannot
// distinguish a peer that has the block from one that says it does and never
// answers. maxSyncStall stays as the wall-clock backstop for that case, and for
// a synchronizer wedged on our own side, where peers are willing and able and
// no block arrives anyway.
func stallVerdictFor(servable bool, stalledFor time.Duration) stallVerdict {
	switch {
	case stalledFor <= syncTimeout:
		return keepSyncing
	case !servable:
		return stopNothingToFetch
	case stalledFor > maxSyncStall:
		return stopStalledTooLong
	default:
		return keepSyncing
	}
}

// MaxPeerHeight returns the highest reported height.
func (s *Synchronizer) MaxPeerHeight() int64 {
	return s.peerStore.MaxHeight()
}

// LastAdvance returns the time when the last block was processed (or start
// time if no blocks were processed).
func (s *Synchronizer) LastAdvance() time.Time {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	return s.lastAdvance
}

// AddPeer records the peer's alleged blockchain base and height. Peers report
// their range repeatedly, so for a peer we already track this only moves that
// range and leaves everything we know about its outstanding requests intact.
func (s *Synchronizer) AddPeer(peer PeerData) {
	s.peerStore.Upsert(peer)
}

// RemovePeer removes the peer with peerID from the synchronizer. If there's no peer
// with peerID, function is a no-op.
func (s *Synchronizer) RemovePeer(peerID types.NodeID) {
	heights := s.dropPeer(peerID)
	// Re-queue once dropPeer released s.mtx: pushBack takes the job generator lock,
	// and holding both serializes every status read behind a peer removal.
	s.jobGen.pushBack(heights...)
}

// dropPeer deletes the peer along with its pending responses and returns the heights
// that have to be fetched again. A dropped response must not be kept: it would make
// addBlock reject the block re-fetched in its place as a duplicate.
func (s *Synchronizer) dropPeer(peerID types.NodeID) []int64 {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	var heights []int64
	for height, resp := range s.pendingToApply {
		if resp.PeerID == peerID {
			delete(s.pendingToApply, height)
			heights = append(heights, height)
		}
	}
	s.peerStore.Delete(peerID)
	return heights
}

// applyBlock applies pending responses in height order until the next height is
// missing. On failure it returns the response that failed to apply, so the caller
// can charge the peer that supplied it rather than an arbitrary one.
func (s *Synchronizer) applyBlock(ctx context.Context) (BlockResponse, error) {
	for {
		resp, ok := s.getPendingResponse()
		if !ok {
			return BlockResponse{}, nil
		}
		err := s.applier.Apply(ctx, resp.Block, resp.Commit)
		if err != nil {
			return resp, fmt.Errorf("cannot apply block: %w", err)
		}
		s.advance()
		s.updateMonitor()
	}
}

func (s *Synchronizer) getPendingResponse() (BlockResponse, bool) {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	resp, ok := s.pendingToApply[s.height]
	return resp, ok
}

func (s *Synchronizer) advance() {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	delete(s.pendingToApply, s.height)
	s.height++
	s.lastAdvance = s.clock.Now()
}

func (s *Synchronizer) updateMonitor() {
	height, syncRate, ok := s.recordSyncRate()
	if !ok {
		return
	}
	// read after recordSyncRate has released the lock, and only once we know we
	// are going to report: this is called for every applied block
	maxPeerHeight := s.peerStore.MaxHeight()
	keyvals := []interface{}{
		"height", height,
		"max_peer_height", maxPeerHeight,
		"blocks/s", syncRate,
	}
	// blocks are applied one at a time, so the per-stage averages say whether the
	// rate above is limited by serialization, signature verification, disk or the
	// ABCI application
	if timings, measured := s.applier.Timings(); measured {
		// logged as durations rather than whole milliseconds: part set building
		// and commit verification are often sub-millisecond, and truncating them
		// to 0 would hide exactly the stages this is here to measure
		keyvals = append(keyvals,
			"partset", timings.PartSet,
			"verify", timings.Verify,
			"save", timings.Save,
			// exec, not abci: the span also covers SaveABCIResponses, the state
			// store write and the mempool update, which are ours, not the app's
			"exec", timings.Exec,
		)
	}
	s.logger.Info("block sync rate", keyvals...)
}

// recordSyncRate updates the smoothed block sync rate, once every
// monitorInterval blocks. It reports false when the current height is not a
// reporting point, in which case nothing was updated.
func (s *Synchronizer) recordSyncRate() (height int64, rate float64, ok bool) {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	if s.monitorInterval <= 0 {
		return 0, 0, false
	}
	progress := s.height - s.startHeight
	if progress <= 0 || progress%s.monitorInterval != 0 {
		return 0, 0, false
	}
	elapsed := s.clock.Since(s.lastMonitorUpdate).Seconds()
	if elapsed <= 0 {
		return 0, 0, false
	}
	// lastSyncRate is updated every monitorInterval blocks using an adaptive filter
	// to smooth the block sync rate. The value represents blocks per second.
	newSyncRate := float64(s.monitorInterval) / elapsed
	if s.lastSyncRate == 0 {
		s.lastSyncRate = newSyncRate
	} else {
		s.lastSyncRate = 0.9*s.lastSyncRate + 0.1*newSyncRate
	}
	s.lastMonitorUpdate = s.clock.Now()
	return s.height, s.lastSyncRate, true
}

// addBlock stores a block response until every lower height has been applied.
// Only heights from the current one up are ever read again, so a response for an
// already applied or already pending height is rejected with errDuplicateBlock.
// TODO: ensure that blocks come in order for each peer.
func (s *Synchronizer) addBlock(resp BlockResponse) error {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	block := resp.Block
	if block.Height < s.height {
		return fmt.Errorf("%w (peer: %s, block height: %d already applied)",
			errDuplicateBlock, resp.PeerID, block.Height)
	}
	_, ok := s.pendingToApply[block.Height]
	if ok {
		return fmt.Errorf("%w (peer: %s, block height: %d)", errDuplicateBlock, resp.PeerID, block.Height)
	}
	s.pendingToApply[block.Height] = resp
	return nil
}

func (s *Synchronizer) removeTimedoutPeers(ctx context.Context) {
	peers := s.peerStore.FindTimedoutPeers()
	for _, peer := range peers {
		s.RemovePeer(peer.peerID)
		curRate := peer.recvMonitor.CurrentTransferRate()
		err := errors.New("peer is not sending us data fast enough")
		s.logger.Error("SendTimeout", "peer", peer.peerID,
			"reason", "peer is not sending us data fast enough",
			"curRate", fmt.Sprintf("%d KB/s", curRate/1024),
			"minRate", fmt.Sprintf("%d KB/s", minRecvRate/1024),
		)
		_ = s.client.Send(ctx, p2p.PeerError{NodeID: peer.peerID, Err: err})
	}
}

func (s *Synchronizer) targetSyncBlocks() int64 {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	return s.peerStore.MaxHeight() - s.startHeight + 1
}

func (s *Synchronizer) getLastSyncRate() float64 {
	s.mtx.RLock()
	defer s.mtx.RUnlock()

	return s.lastSyncRate
}

func (s *Synchronizer) runHandler(ctx context.Context, handler func(ctx context.Context) error) {
	// Drive the loop off the context, not IsRunning(): OnStart spawns this
	// goroutine before BaseService marks the service running, so reading
	// IsRunning() here could observe false and exit at birth, wedging sync.
	for ctx.Err() == nil {
		if err := handler(ctx); errors.Is(err, workerpool.ErrWorkerPoolStopped) {
			// The pool is stopping; exit instead of busy-spinning and flooding logs.
			return
		}
	}
}

// BlockResponse ...
type BlockResponse struct {
	PeerID types.NodeID
	Block  *types.Block
	Commit *types.Commit
	// Size is the serialized size of Block in bytes, measured while decoding the
	// response. Deriving it from Block again means re-serializing the whole
	// block, which is too expensive to do on the block apply path.
	Size int
}

func (r *BlockResponse) Validate() error {
	if r.Block == nil {
		return errors.New("block response without a block")
	}
	if r.Commit == nil {
		// See https://github.com/tendermint/tendermint/pull/8433#discussion_r866790631
		return fmt.Errorf("a block without a commit at height %d - possible node store corruption", r.Block.Height)
	}
	if r.Block.Height != r.Commit.Height {
		return fmt.Errorf("heights don't match, not adding block (block height: %d, commit height: %d)",
			r.Block.Height,
			r.Commit.Height)
	}
	return nil
}

func BlockResponseFromProto(resp *bcproto.BlockResponse, peerID types.NodeID) (*BlockResponse, error) {
	if resp == nil {
		return nil, errors.New("invalid")
	}
	block, err := types.BlockFromProto(resp.Block)
	if err != nil {
		return nil, err
	}
	var commit *types.Commit
	if resp.Commit != nil {
		commit, err = types.CommitFromProto(resp.Commit)
		if err != nil {
			return nil, err
		}
	}
	return &BlockResponse{
		PeerID: peerID,
		Block:  block,
		Commit: commit,
		// measured here, on a worker goroutine, rather than on the single
		// goroutine that applies blocks
		Size: resp.Block.Size(),
	}, nil
}
