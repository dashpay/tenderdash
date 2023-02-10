package blocksync

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/tendermint/tendermint/internal/p2p"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/libs/service"
	"github.com/tendermint/tendermint/libs/workerpool"
	bcproto "github.com/tendermint/tendermint/proto/tendermint/blocksync"
	"github.com/tendermint/tendermint/types"
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
	requestInterval           = 2 * time.Millisecond
	maxTotalRequesters        = 600
	maxPeerErrBuffer          = 1000
	maxPendingRequests        = maxTotalRequesters
	maxPendingRequestsPerPeer = 20

	// Minimum recv rate to ensure we're receiving blocks from a peer fast
	// enough. If a peer is not sending us data at at least that rate, we
	// consider them to have timedout and we disconnect.
	//
	// Assuming a DSL connection (not a good choice) 128 Kbps (upload) ~ 15 KB/s,
	// sending data across atlantic ~ 7.5 KB/s.
	minRecvRate = 7680

	// Maximum difference between current and new block's height.
	maxDiffBetweenCurrentAndReceivedBlockHeight = 100

	peerTimeout = 15 * time.Second
)

var (
	errPeerNotResponded = errors.New("peer did not send us anything")
)

/*
	Peers self report their heights when we join the block pool.
	Starting from our latest pool.height, we request blocks
	in sequence from peers that reported higher heights than ours.
	Every so often we ask peers what height they're on so we can keep going.

	Requests are continuously made for blocks of higher heights until
	the limit is reached. If most of the requests have no available peers, and we
	are not at peer limits, we can probably switch to consensus reactor
*/

// BlockPool keeps track of the block sync peers, block requests and block responses.
type BlockPool struct {
	service.BaseService
	logger log.Logger

	lastAdvance time.Time

	mtx sync.RWMutex

	height int64 // the lowest key in requesters.
	// peers
	maxPeerHeight int64 // the biggest reported height

	// atomic
	jobProgressCounter atomic.Int32 // number of requests pending assignment or block response

	startHeight      int64
	lastHundredBlock time.Time
	lastSyncRate     float64

	peerStore      *InMemPeerStore
	client         BlockClient
	applier        *blockApplier
	workerPool     *workerpool.WorkerPool
	jobGen         *jobGenerator
	pendingToApply map[int64]BlockResponse
}

type OptionFunc func(v *BlockPool)

func WithWorkerPool(wp *workerpool.WorkerPool) OptionFunc {
	return func(v *BlockPool) {
		v.workerPool = wp
	}
}

func WithLogger(logger log.Logger) OptionFunc {
	return func(v *BlockPool) {
		v.logger = logger
	}
}

// NewBlockPool returns a new BlockPool with the height equal to start. Block
// requests and errors will be sent to requestsCh and errorsCh accordingly.
func NewBlockPool(start int64, client BlockClient, blockExec *blockApplier, opts ...OptionFunc) *BlockPool {
	peerStore := NewInMemPeerStore()
	logger := log.NewNopLogger()
	bp := &BlockPool{
		logger:         logger,
		client:         client,
		applier:        blockExec,
		peerStore:      peerStore,
		jobGen:         newJobGenerator(start, logger, client, peerStore),
		startHeight:    start,
		height:         start,
		lastSyncRate:   0,
		workerPool:     workerpool.New(100),
		pendingToApply: map[int64]BlockResponse{},
	}
	for _, opt := range opts {
		opt(bp)
	}
	bp.BaseService = *service.NewBaseService(logger, "BlockPool", bp)
	return bp
}

// OnStart implements service.Service by spawning requesters routine and recording
// pool's start time.
func (pool *BlockPool) OnStart(ctx context.Context) error {
	pool.lastAdvance = time.Now()
	pool.lastHundredBlock = pool.lastAdvance
	pool.workerPool.Run(ctx)
	go func() {
		for pool.IsRunning() {
			select {
			case <-ctx.Done():
				return
			default:
				pool.produceJob(ctx)
			}
		}
	}()
	go func() {
		for pool.IsRunning() {
			select {
			case <-ctx.Done():
				return
			default:
				pool.consumeJobResult(ctx)
			}
		}
	}()
	return nil
}

func (*BlockPool) OnStop() {}

func (pool *BlockPool) produceJob(ctx context.Context) {
	if !pool.jobGen.shouldJobBeGenerated(pool.maxPeerHeight) {
		// TODO should we stop producer loop ?
		return
	}
	// remove timed out peers and redo its heights again
	pool.removeTimedoutPeers(ctx)
	pool.jobProgressCounter.Add(1)
	job, err := pool.jobGen.nextJob(ctx)
	if err != nil {
		pool.logger.Error("cannot create a next job", "error", err)
		return
	}
	pool.workerPool.Add(job)
}

func (pool *BlockPool) consumeJobResult(ctx context.Context) {
	res, err := pool.workerPool.Receive(ctx)
	if err != nil {
		pool.logger.Error("cannot receive a job result from worker pool", "error", err)
		return
	}
	pool.jobProgressCounter.Add(-1)
	if res.Err != nil {
		var bfErr *errBlockFetch
		if !errors.As(res.Err, &bfErr) {
			return
		}
		pool.jobGen.pushBack(bfErr.height)
		pool.RemovePeer(bfErr.peerID)
		_ = pool.client.Send(ctx, p2p.PeerError{NodeID: bfErr.peerID, Err: bfErr})
		return
	}
	resp := res.Value.(*BlockResponse)
	err = pool.addBlock(*resp)
	if err != nil {
		_ = pool.client.Send(ctx, p2p.PeerError{NodeID: resp.PeerID, Err: err})
		return
	}
	err = pool.applyBlock(ctx)
	if err != nil {
		pool.logger.Error("cannot apply block", "height", resp.Block.Height, "error", err.Error())
		pool.RemovePeer(resp.PeerID)
		_ = pool.client.Send(ctx, p2p.PeerError{NodeID: resp.PeerID, Err: err})
	}
}

func (pool *BlockPool) applyBlock(ctx context.Context) error {
	for {
		resp, ok := pool.pendingToApply[pool.height]
		if !ok {
			return nil
		}
		err := pool.applier.Apply(ctx, resp.Block, resp.Commit)
		if err != nil {
			return fmt.Errorf("cannot apply block: %w", err)
		}
		delete(pool.pendingToApply, pool.height)
		pool.height++
		pool.lastAdvance = time.Now()

		diff := pool.height - pool.startHeight
		if diff%100 == 0 {
			// the lastSyncRate will be updated every 100 blocks, it uses the adaptive filter
			// to smooth the block sync rate and the unit represents the number of blocks per second.
			pool.lastSyncRate = 0.9*pool.lastSyncRate + 0.1*(100/time.Since(pool.lastHundredBlock).Seconds())
			pool.logger.Info(
				"block sync rate",
				"height", pool.height,
				"max_peer_height", pool.maxPeerHeight,
				"blocks/s", pool.lastSyncRate,
			)
			pool.lastHundredBlock = time.Now()
		}
	}
}

// GetStatus returns pool's height, count of in progress requests
func (pool *BlockPool) GetStatus() (height int64, numPending int32) {
	pool.mtx.RLock()
	defer pool.mtx.RUnlock()
	return pool.height, pool.jobProgressCounter.Load()
}

// IsCaughtUp returns true if this node is caught up, false - otherwise.
func (pool *BlockPool) IsCaughtUp() bool {
	pool.mtx.RLock()
	defer pool.mtx.RUnlock()
	// Need at least 1 peer to be considered caught up.
	if pool.peerStore.IsZero() {
		return false
	}
	// NOTE: we use maxPeerHeight - 1 because to sync block H requires block H+1
	// to verify the LastCommit.
	return pool.height >= (pool.maxPeerHeight - 1)
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
func (pool *BlockPool) addBlock(resp BlockResponse) error {
	pool.mtx.Lock()
	defer pool.mtx.Unlock()

	block := resp.Block
	_, ok := pool.pendingToApply[block.Height]
	if ok {
		return fmt.Errorf("block response already exists (peer: %s, block height: %d)", resp.PeerID, block.Height)
	}
	// TODO doubt this checking is necessary
	diff := math.Abs(float64(pool.height - block.Height))
	if diff > maxDiffBetweenCurrentAndReceivedBlockHeight {
		return errors.New("peer sent us a block we didn't expect with a height too far ahead/behind")
	}
	pool.pendingToApply[block.Height] = resp
	return nil
}

// MaxPeerHeight returns the highest reported height.
func (pool *BlockPool) MaxPeerHeight() int64 {
	pool.mtx.RLock()
	defer pool.mtx.RUnlock()
	return pool.maxPeerHeight
}

// LastAdvance returns the time when the last block was processed (or start
// time if no blocks were processed).
func (pool *BlockPool) LastAdvance() time.Time {
	pool.mtx.RLock()
	defer pool.mtx.RUnlock()
	return pool.lastAdvance
}

// SetPeerRange sets the peer's alleged blockchain base and height.
func (pool *BlockPool) SetPeerRange(peer *PeerData) {
	pool.mtx.Lock()
	defer pool.mtx.Unlock()
	pool.peerStore.Put(peer)
	if peer.height > pool.maxPeerHeight {
		pool.maxPeerHeight = peer.height
	}
}

// RemovePeer removes the peer with peerID from the pool. If there's no peer
// with peerID, function is a no-op.
func (pool *BlockPool) RemovePeer(peerID types.NodeID) {
	pool.mtx.Lock()
	defer pool.mtx.Unlock()
	pool.removePeer(peerID)
}

func (pool *BlockPool) removePeer(peerID types.NodeID) {
	for _, resp := range pool.pendingToApply {
		if resp.PeerID == peerID {
			pool.jobGen.pushBack(resp.Block.Height)
		}
	}
	peer, ok := pool.peerStore.GetAndRemove(peerID)
	if !ok {
		return
	}
	if peer.height == pool.maxPeerHeight {
		pool.maxPeerHeight = pool.peerStore.MaxPeerHeight()
	}
}

func (pool *BlockPool) removeTimedoutPeers(ctx context.Context) {
	peers := pool.peerStore.FindTimedoutPeers()
	for _, peer := range peers {
		pool.RemovePeer(peer.peerID)
		curRate := peer.recvMonitor.CurrentTransferRate()
		err := errors.New("peer is not sending us data fast enough")
		pool.logger.Error("SendTimeout", "peer", peer.peerID,
			"reason", "peer is not sending us data fast enough",
			"curRate", fmt.Sprintf("%d KB/s", curRate/1024),
			"minRate", fmt.Sprintf("%d KB/s", minRecvRate/1024),
		)
		_ = pool.client.Send(ctx, p2p.PeerError{NodeID: peer.peerID, Err: err})
	}
}

func (pool *BlockPool) targetSyncBlocks() int64 {
	pool.mtx.RLock()
	defer pool.mtx.RUnlock()

	return pool.maxPeerHeight - pool.startHeight + 1
}

func (pool *BlockPool) getLastSyncRate() float64 {
	pool.mtx.RLock()
	defer pool.mtx.RUnlock()

	return pool.lastSyncRate
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
