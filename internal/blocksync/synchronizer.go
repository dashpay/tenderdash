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

	// Minimum recv rate to ensure we're receiving blocks from a peer fast
	// enough. If a peer is not sending us data at at least that rate, we
	// consider them to have timed out and we disconnect.
	//
	// Assuming a DSL connection (not a good choice) 128 Kbps (upload) ~ 15 KB/s,
	// sending data across atlantic ~ 7.5 KB/s.
	minRecvRate = 7680
)

// errDuplicateBlock is returned when a block response arrives for a height that
// is already pending. It means we requested the height more than once, so it
// must not be reported back to the peer that answered.
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
	if !s.jobGen.shouldJobBeGenerated() {
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
		s.RemovePeer(bfErr.peerID)
		_ = s.client.Send(ctx, p2p.PeerError{NodeID: bfErr.peerID, Err: bfErr})
		return nil
	}
	resp := res.Value.(*BlockResponse)
	s.peerStore.Update(resp.PeerID, AddNumPending(-1), UpdateMonitor(resp.Size))
	err = s.addBlock(*resp)
	if err != nil {
		if !errors.Is(err, errDuplicateBlock) {
			s.logger.Error("cannot add a block to the pending list",
				"height", resp.Block.Height,
				"error", err.Error())
			_ = s.client.Send(ctx, p2p.PeerError{NodeID: resp.PeerID, Err: err})
			return nil
		}
		// A duplicate is not the sending peer's fault, we asked more than one peer
		// for this height. Drop the block, but carry on applying what we can.
		s.logger.Debug("dropping duplicate block response",
			"height", resp.Block.Height,
			"peer", resp.PeerID)
	}
	err = s.applyBlock(ctx)
	if err != nil {
		s.logger.Error("cannot apply a block", "height", resp.Block.Height, "error", err.Error())
		s.RemovePeer(resp.PeerID)
		_ = s.client.Send(ctx, p2p.PeerError{NodeID: resp.PeerID, Err: err})
	}
	return nil
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

func (s *Synchronizer) WaitForSync(ctx context.Context) {
	ticker := time.NewTicker(switchToConsensusIntervalSeconds * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var (
				height, _   = s.GetStatus()
				lastAdvance = s.LastAdvance()
				isCaughtUp  = s.IsCaughtUp()
			)
			if isCaughtUp || time.Since(lastAdvance) > syncTimeout {
				return
			}
			s.logger.Info(
				"not caught up yet",
				"height", height,
				"max_peer_height", s.MaxPeerHeight(),
				"timeout_in", syncTimeout-time.Since(lastAdvance),
			)
		}
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

// AddPeer adds the peer's alleged blockchain base and height
func (s *Synchronizer) AddPeer(peer PeerData) {
	s.peerStore.Put(peer.peerID, peer)
}

// RemovePeer removes the peer with peerID from the synchronizer. If there's no peer
// with peerID, function is a no-op.
func (s *Synchronizer) RemovePeer(peerID types.NodeID) {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	s.removePeer(peerID)
}

func (s *Synchronizer) removePeer(peerID types.NodeID) {
	// Blocks already received from this peer are dropped and re-requested, since
	// the peer is being removed precisely because we stopped trusting it to serve
	// us good data in good time. The pending entry has to be deleted along with
	// the re-request: leaving it behind makes addBlock reject the re-fetched block
	// as a duplicate, which in turn punishes the peer that served it correctly.
	for height, resp := range s.pendingToApply {
		if resp.PeerID == peerID {
			delete(s.pendingToApply, height)
			s.jobGen.pushBack(height)
		}
	}
	s.peerStore.Delete(peerID)
}

func (s *Synchronizer) applyBlock(ctx context.Context) error {
	for {
		resp, ok := s.getPendingResponse()
		if !ok {
			return nil
		}
		err := s.applier.Apply(ctx, resp.Block, resp.Commit)
		if err != nil {
			return fmt.Errorf("cannot apply block: %w", err)
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

// addBlock validates that the block comes from the peer it was expected from
// and calls the requester to store it.
//
// This requires an extended commit at the same height as the supplied block -
// the block contains the last commit, but we need the latest commit in case we
// need to switch over from block sync to consensus at this height. If the
// height of the extended commit and the height of the block do not match, we
// do not add the block and return an error.
// TODO: ensure that blocks come in order for each peer.
func (s *Synchronizer) addBlock(resp BlockResponse) error {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	block := resp.Block
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
