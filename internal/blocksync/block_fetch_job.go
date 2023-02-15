package blocksync

import (
	"context"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/libs/workerpool"
	"github.com/tendermint/tendermint/types"
)

type (
	errBlockFetch struct {
		peerID types.NodeID
		height int64
		err    error
	}
	blockFetchJob struct {
		logger log.Logger
		client BlockClient
		peer   PeerData
		height int64
	}
)

func (e *errBlockFetch) Error() string {
	return e.err.Error()
}

// Execute ...
func (j *blockFetchJob) Execute(ctx context.Context) workerpool.Result {
	promise, err := j.client.GetBlock(ctx, j.height, j.peer.peerID)
	if err != nil {
		return j.errorResult(j.peer.peerID, j.height, err)
	}
	protoResp, err := promise.Await()
	if err != nil {
		return j.errorResult(j.peer.peerID, j.height, err)
	}
	resp, err := BlockResponseFromProto(protoResp, j.peer.peerID)
	if err != nil {
		return j.errorResult(j.peer.peerID, j.height, err)
	}
	err = resp.Validate()
	if err != nil {
		return j.errorResult(j.peer.peerID, j.height, err)
	}
	return workerpool.Result{Value: resp}
}

func (j *blockFetchJob) errorResult(peerID types.NodeID, height int64, err error) workerpool.Result {
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
	client     BlockClient
	peerStore  *InMemPeerStore
	height     int64
	pushedBack []int64
}

func newJobGenerator(height int64, logger log.Logger, client BlockClient, peerStore *InMemPeerStore) *jobGenerator {
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

func (p *jobGenerator) nextJob(ctx context.Context) (*blockFetchJob, error) {
	height := p.nextHeight()
	peer, err := p.getPeer(ctx, height)
	if err != nil {
		return nil, err
	}
	return &blockFetchJob{
		logger: p.logger,
		client: p.client,
		peer:   peer,
		height: height,
	}, nil
}

func (p *jobGenerator) pushBack(height int64) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
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
