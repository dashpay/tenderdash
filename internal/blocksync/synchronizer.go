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
	s.workerPool.Run(ctx)
	go s.runHandler(ctx, s.produceJob)
	go s.runHandler(ctx, s.consumeJobResult)
	return nil
}

func (s *Synchronizer) OnStop() {
	s.workerPool.Stop(context.Background())
}

func (s *Synchronizer) produceJob(ctx context.Context) {
	if !s.jobGen.shouldJobBeGenerated() {
		// TODO should we stop producer loop ?
		// TODO need to come up with a smarter way how to produce jobs without sleeping
		s.clock.Sleep(50 * time.Millisecond)
		return
	}
	// remove timed out peers and redo its heights again
	s.removeTimedoutPeers(ctx)
	s.jobProgressCounter.Add(1)
	job, err := s.jobGen.nextJob(ctx)
	if err != nil {
		s.logger.Error("cannot create a next job", "error", err)
		return
	}
	err = s.workerPool.Send(ctx, job)
	if err != nil {
		s.logger.Error("cannot add a job to worker-pool", "error", err)
	}
}

func (s *Synchronizer) consumeJobResult(ctx context.Context) {
	res, err := s.workerPool.Receive(ctx)
	if err != nil {
		s.logger.Error("cannot receive a job result from worker pool", "error", err)
		return
	}
	s.jobProgressCounter.Add(-1)
	if res.Err != nil {
		var bfErr *errBlockFetch
		if !errors.As(res.Err, &bfErr) {
			return
		}
		s.jobGen.pushBack(bfErr.height)
		s.RemovePeer(bfErr.peerID)
		_ = s.client.Send(ctx, p2p.PeerError{NodeID: bfErr.peerID, Err: bfErr})
		return
	}
	resp := res.Value.(*BlockResponse)
	s.peerStore.Update(resp.PeerID, AddNumPending(-1), UpdateMonitor(resp.Block.Size()))
	err = s.addBlock(*resp)
	if err != nil {
		s.logger.Error("cannot add a block to the pending list",
			"height", resp.Block.Height,
			"error", err.Error())
		_ = s.client.Send(ctx, p2p.PeerError{NodeID: resp.PeerID, Err: err})
		return
	}
	err = s.applyBlock(ctx)
	if err != nil {
		s.logger.Error("cannot apply a block", "height", resp.Block.Height, "error", err.Error())
		s.RemovePeer(resp.PeerID)
		_ = s.client.Send(ctx, p2p.PeerError{NodeID: resp.PeerID, Err: err})
	}
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
	for _, resp := range s.pendingToApply {
		if resp.PeerID == peerID {
			s.jobGen.pushBack(resp.Block.Height)
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
	s.mtx.Lock()
	defer s.mtx.Unlock()

	if s.monitorInterval <= 0 {
		return
	}
	progress := s.height - s.startHeight
	if progress <= 0 || progress%s.monitorInterval != 0 {
		return
	}
	elapsed := s.clock.Since(s.lastMonitorUpdate).Seconds()
	if elapsed <= 0 {
		return
	}
	// lastSyncRate is updated every monitorInterval blocks using an adaptive filter
	// to smooth the block sync rate. The value represents blocks per second.
	newSyncRate := float64(s.monitorInterval) / elapsed
	if s.lastSyncRate == 0 {
		s.lastSyncRate = newSyncRate
	} else {
		s.lastSyncRate = 0.9*s.lastSyncRate + 0.1*newSyncRate
	}
	s.logger.Info(
		"block sync rate",
		"height", s.height,
		"max_peer_height", s.peerStore.MaxHeight(),
		"blocks/s", s.lastSyncRate,
	)
	s.lastMonitorUpdate = s.clock.Now()
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
		return fmt.Errorf("block response already exists (peer: %s, block height: %d)", resp.PeerID, block.Height)
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

func (s *Synchronizer) runHandler(ctx context.Context, handler func(ctx context.Context)) {
	for s.IsRunning() {
		select {
		case <-ctx.Done():
			return
		default:
			handler(ctx)
		}
	}
}

// BlockResponse ...
type BlockResponse struct {
	PeerID types.NodeID
	Block  *types.Block
	Commit *types.Commit
}

func (r *BlockResponse) Validate() error {
	if r.Commit != nil && r.Block.Height != r.Commit.Height {
		return fmt.Errorf("heights don't match, not adding block (block height: %d, commit height: %d)",
			r.Block.Height,
			r.Commit.Height)
	}
	if r.Block != nil && r.Commit == nil {
		// See https://github.com/tendermint/tendermint/pull/8433#discussion_r866790631
		return fmt.Errorf("a block without a commit at height %d - possible node store corruption", r.Block.Height)
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
	}, nil
}
