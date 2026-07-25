package blocksync

import (
	"context"
	"fmt"
	"slices"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/dashpay/tenderdash/internal/p2p/client"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/workerpool"
	"github.com/dashpay/tenderdash/types"
)

type (
	errBlockFetch struct {
		peerID types.NodeID
		height int64
		err    error
	}
)

func (e *errBlockFetch) Error() string {
	return e.err.Error()
}

// blockFetchJobHandler requests for a block by height from the peer, if the peer responds to the block in time, then the job
// will return it, otherwise the job will return an error
func blockFetchJobHandler(client client.BlockClient, peer PeerData, height int64) workerpool.JobHandler {
	return func(ctx context.Context) workerpool.Result {
		promise, err := client.GetBlock(ctx, height, peer.peerID)
		if err != nil {
			return errorResult(peer.peerID, height, err)
		}
		protoResp, err := promise.Await()
		if err != nil {
			return errorResult(peer.peerID, height, err)
		}
		resp, err := BlockResponseFromProto(protoResp, peer.peerID)
		if err != nil {
			return errorResult(peer.peerID, height, err)
		}
		err = resp.Validate()
		if err != nil {
			return errorResult(peer.peerID, height, err)
		}
		if resp.Block.Height != height {
			return errorResult(peer.peerID, height,
				fmt.Errorf("peer sent block at height %d, requested height %d", resp.Block.Height, height))
		}
		return workerpool.Result{Value: resp}
	}
}

func errorResult(peerID types.NodeID, height int64, err error) workerpool.Result {
	return workerpool.Result{
		Err: &errBlockFetch{
			err:    err,
			height: height,
			peerID: peerID,
		},
	}
}

type jobGenerator struct {
	mtx        sync.RWMutex
	logger     log.Logger
	client     client.BlockClient
	peerStore  *InMemPeerStore
	height     int64
	pushedBack []int64
}

func newJobGenerator(height int64, logger log.Logger, client client.BlockClient, peerStore *InMemPeerStore) *jobGenerator {
	return &jobGenerator{
		logger:     logger,
		client:     client,
		peerStore:  peerStore,
		height:     height,
		pushedBack: nil,
	}
}

func (p *jobGenerator) nextHeight() int64 {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	if len(p.pushedBack) > 0 {
		height := p.pushedBack[0]
		p.pushedBack = p.pushedBack[1:]
		return height
	}
	height := p.height
	p.height++
	return height
}

func (p *jobGenerator) nextJob(ctx context.Context) (*workerpool.Job, error) {
	height := p.nextHeight()
	peer, err := p.getPeer(ctx, height)
	if err != nil {
		return nil, err
	}
	p.peerStore.Update(peer.peerID, ResetMonitor(), AddNumPending(1))
	return workerpool.NewJob(blockFetchJobHandler(p.client, peer, height)), nil
}

// pushBack schedules a height to be fetched again. Heights already scheduled are
// ignored: fetching one twice makes the second response arrive as a duplicate,
// which costs a round trip and risks punishing the peer that served it.
func (p *jobGenerator) pushBack(height int64) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	if slices.Contains(p.pushedBack, height) {
		return
	}
	p.pushedBack = append(p.pushedBack, height)
}

func (p *jobGenerator) getPeer(ctx context.Context, height int64) (PeerData, error) {
	for {
		if ctx.Err() != nil {
			return PeerData{}, ctx.Err()
		}
		peer, found := p.peerStore.FindPeer(height)
		if found {
			return peer, nil
		}
		// This is preferable to using a timer because the request
		// interval is so small. Larger request intervals may
		// necessitate using a timer/ticker.
		time.Sleep(requestInterval)
	}
}

func (p *jobGenerator) shouldJobBeGenerated() bool {
	p.mtx.RLock()
	defer p.mtx.RUnlock()
	if len(p.pushedBack) > 0 {
		return true
	}
	return p.height <= p.peerStore.MaxHeight()
}
