package blocksync

import (
	"context"
	"errors"
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
		connID uint64
		height int64
		err    error
	}
)

func (e *errBlockFetch) Error() string {
	return e.err.Error()
}

// blockFetchJobHandler requests for a block by height from the peer, if the peer responds to the block in time, then the job
// will return it, otherwise the job will return an error
func blockFetchJobHandler(blockClient client.BlockClient, peer PeerData, height int64) workerpool.JobHandler {
	return func(ctx context.Context) workerpool.Result {
		observation := peer.service.request()
		size := 0
		defer func() { observation.complete(size) }()
		ctx = client.WithBlockRequestObserver(ctx, observation)
		promise, err := blockClient.GetBlock(ctx, height, peer.peerID)
		if err != nil {
			return errorResult(peer, height, err)
		}
		protoResp, err := promise.Await()
		responder := peer
		responder.connID = attributedConn(peer.connID, observation, err)
		if err != nil {
			return errorResult(responder, height, err)
		}
		resp, err := BlockResponseFromProto(protoResp, peer.peerID)
		if err != nil {
			return errorResult(responder, height, err)
		}
		err = resp.Validate()
		if err != nil {
			return errorResult(responder, height, err)
		}
		if resp.Block.Height != height {
			return errorResult(responder, height,
				fmt.Errorf("peer sent block at height %d, requested height %d", resp.Block.Height, height))
		}
		resp.ConnID = responder.connID
		size = protoResp.Size()
		return workerpool.Result{Value: resp}
	}
}

// attributedConn returns the generation a request's outcome is attributed to: the
// connection that delivered the response, or the reservation's when there is none.
// A timeout can race a late delivery, so only an answer may read the delivered one;
// that keeps a report's generation independent of how the race fell.
func attributedConn(reserved uint64, request *serviceRequest, awaitErr error) uint64 {
	var missing *client.ErrBlockNotFound
	if awaitErr != nil && !errors.As(awaitErr, &missing) {
		return reserved
	}
	if delivered := request.deliveredConnID(); delivered != 0 {
		return delivered
	}
	return reserved
}

func errorResult(peer PeerData, height int64, err error) workerpool.Result {
	return workerpool.Result{
		Err: &errBlockFetch{
			err:    err,
			height: height,
			peerID: peer.peerID,
			connID: peer.connID,
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
	// applyHeight is the height block sync waits to apply. Once a request for it
	// fails, or goes unanswered for serviceGrace, every peer that advertises it
	// is asked for it; spreading says some peer is left to ask. requests holds
	// what was asked of whom for each height from applyHeight up.
	applyHeight int64
	spreading   bool
	requests    map[int64]*heightRequests
	// applyState, when set, reports the height to apply and whether its block
	// has arrived, so that a wait for a peer inside nextJob sees them.
	applyState func() (height int64, received bool)
}

type heightRequests struct {
	// since is when the height was first requested.
	since  time.Time
	asked  map[types.NodeID]struct{}
	failed bool
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
	height, _ := p.next()
	return height
}

// next returns the next height to request and whether it is a retry.
func (p *jobGenerator) next() (int64, bool) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	if len(p.pushedBack) > 0 {
		height := p.pushedBack[0]
		p.pushedBack = p.pushedBack[1:]
		return height, true
	}
	height := p.height
	p.height++
	return height, false
}

func (p *jobGenerator) nextJob(ctx context.Context) (*workerpool.Job, error) {
	for {
		if peer, height, found := p.nextSpread(); found {
			return workerpool.NewJob(blockFetchJobHandler(p.client, peer, height)), nil
		}
		height, retry := p.next()
		peer, err := p.getPeer(ctx, height)
		if errors.Is(err, errLowerHeightQueued) {
			p.giveBack(height, retry)
			continue
		}
		if err != nil {
			return nil, err
		}
		p.markRequested(height, peer.peerID)
		return workerpool.NewJob(blockFetchJobHandler(p.client, peer, height)), nil
	}
}

// giveBack returns a height taken by next and not requested, as a retry if it
// was one and otherwise as the next new height.
func (p *jobGenerator) giveBack(height int64, retry bool) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	if !retry && p.height == height+1 {
		p.height = height
		return
	}
	p.queue(height)
}

// markRequested records that height was asked of peerID.
func (p *jobGenerator) markRequested(height int64, peerID types.NodeID) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	if rec := p.requestsFor(height); rec != nil {
		rec.asked[peerID] = struct{}{}
	}
}

// requestsFor returns the record of requests for height, created if missing,
// or nil for a height below the one to apply. The caller holds p.mtx.
func (p *jobGenerator) requestsFor(height int64) *heightRequests {
	if height < p.applyHeight {
		return nil
	}
	rec, found := p.requests[height]
	if !found {
		if p.requests == nil {
			p.requests = map[int64]*heightRequests{}
		}
		rec = &heightRequests{since: p.peerStore.clock.Now(), asked: map[types.NodeID]struct{}{}}
		p.requests[height] = rec
	}
	return rec
}

// observe takes note of the height to apply and whether its block has arrived,
// and decides whether to ask every peer that advertises it: once a request for
// it failed or has gone unanswered for serviceGrace. One peer after another,
// peers ranked first could each hold it until it times out. The caller holds
// p.mtx.
func (p *jobGenerator) observe(applyHeight int64, received bool) {
	if applyHeight > p.applyHeight {
		p.applyHeight = applyHeight
		for height := range p.requests {
			if height < applyHeight {
				delete(p.requests, height)
			}
		}
	}
	rec := p.requests[p.applyHeight]
	p.spreading = false
	if rec == nil || received || !rec.failed && p.peerStore.clock.Since(rec.since) <= serviceGrace {
		return
	}
	_, p.spreading = p.peerStore.findPeer(p.applyHeight, rec.asked)
}

// nextSpread reserves one more peer, not asked yet, for the height block sync
// waits to apply, and reports false once there is none. A retry of that height
// queued after a failure goes first.
func (p *jobGenerator) nextSpread() (PeerData, int64, bool) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	if !p.spreading || len(p.pushedBack) > 0 && p.pushedBack[0] == p.applyHeight {
		return PeerData{}, 0, false
	}
	rec := p.requestsFor(p.applyHeight)
	peer, found := p.peerStore.reservePeerExcept(p.applyHeight, rec.asked)
	if !found {
		p.spreading = false
		return PeerData{}, 0, false
	}
	rec.asked[peer.peerID] = struct{}{}
	return peer, p.applyHeight, true
}

// errLowerHeightQueued reports that a height below the one waiting for a peer
// was queued for a retry meanwhile.
var errLowerHeightQueued = errors.New("a lower height is queued")

// pushBack schedules heights whose requests failed to be fetched again, skipping
// those already queued so a queued height is not fetched twice. Heights already
// handed to a worker are not tracked here, so an in-flight height can still be
// queued and fetched again.
//
// The queue is kept sorted because nextHeight pops from the front and callers hand
// us heights in arbitrary order - dropPeer collects them by ranging a map. Only the
// lowest missing height unblocks applyBlock, so retrying in arrival order can spend
// the whole no-progress window fetching heights that cannot be applied yet.
//
// Membership is built once per call rather than rescanning the queue per height,
// which would be quadratic in the size of a batch.
func (p *jobGenerator) pushBack(heights ...int64) {
	if len(heights) == 0 {
		return
	}
	p.mtx.Lock()
	defer p.mtx.Unlock()
	for _, height := range heights {
		if rec := p.requestsFor(height); rec != nil {
			rec.failed = true
		}
	}
	p.queue(heights...)
}

// requeue schedules height to be fetched again after a peer answered that it
// has no such block. Another peer is asked; that alone is no reason to ask all.
func (p *jobGenerator) requeue(height int64) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	p.queue(height)
}

// queue adds heights to the retry queue. The caller holds p.mtx.
func (p *jobGenerator) queue(heights ...int64) {
	queued := make(map[int64]struct{}, len(p.pushedBack)+len(heights))
	for _, height := range p.pushedBack {
		queued[height] = struct{}{}
	}
	p.pushedBack = slices.Grow(p.pushedBack, len(heights))
	for _, height := range heights {
		if _, ok := queued[height]; ok {
			continue
		}
		queued[height] = struct{}{}
		p.pushedBack = append(p.pushedBack, height)
	}
	slices.Sort(p.pushedBack)
}

// lowerQueued reports whether a height below height is due to be requested: a
// retry, or the height to apply to be asked of more peers.
func (p *jobGenerator) lowerQueued(height int64) bool {
	var (
		applyHeight int64
		received    bool
	)
	if p.applyState != nil {
		applyHeight, received = p.applyState()
	}
	p.mtx.Lock()
	defer p.mtx.Unlock()
	if p.applyState != nil {
		p.observe(applyHeight, received)
	}
	return len(p.pushedBack) > 0 && p.pushedBack[0] < height ||
		p.spreading && p.applyHeight < height
}

func (p *jobGenerator) getPeer(ctx context.Context, height int64) (PeerData, error) {
	for {
		if ctx.Err() != nil {
			return PeerData{}, ctx.Err()
		}
		peer, found := p.peerStore.reservePeer(height)
		if found {
			return peer, nil
		}
		// Blocks are applied in order, so waiting on a higher height for a free
		// peer must not hold back a lower one that has to be fetched again.
		if p.lowerQueued(height) {
			return PeerData{}, errLowerHeightQueued
		}
		// This is preferable to using a timer because the request
		// interval is so small. Larger request intervals may
		// necessitate using a timer/ticker.
		time.Sleep(requestInterval)
	}
}

// shouldJobBeGenerated reports whether another height may be requested now.
// applyHeight is the height block sync is waiting to apply and pendingBytes is
// the size of the responses already held.
//
// Blocks are applied strictly in order, so a response that arrives before the
// height below it has been applied is held until that one does, and nothing
// else limits how many are held: a peer's in-flight request count is released
// when its response arrives, not when its block is applied. While blocks are
// being applied the whole pipeline throttles itself - the consumer applies
// synchronously, so the result channel fills, the workers block and this loop
// stalls - but a single missing height removes precisely that, leaving what is
// held bounded only by how far above us peers claim to be.
//
// Two limits replace that. What is already held is capped by bytes, which is
// the cost that matters. What may still be requested is capped by a window of
// maxOutstandingHeights heights above applyHeight, because a request in flight
// has no size to charge yet and every one of them will be held the moment the
// height below it goes missing. The window only moves up, and addBlock rejects
// anything below applyHeight, so nothing can be held from outside it.
//
// Heights queued for a retry are handed out whatever either limit says. Each
// was inside the window when it was first requested and the window only moves
// up, so re-issuing one cannot widen the backlog; and the lowest of them is
// typically the very height everything else is waiting for, so holding those
// back is what would turn a bound into a permanent stall.
func (p *jobGenerator) shouldJobBeGenerated(applyHeight int64, pendingBytes int) bool {
	received := false
	if p.applyState != nil {
		if height, arrived := p.applyState(); height == applyHeight {
			received = arrived
		}
	}
	p.mtx.Lock()
	defer p.mtx.Unlock()
	// Asking one more peer is a job only while one is left to ask; otherwise
	// nextJob would take a new height past the limits below.
	if p.observe(applyHeight, received); p.spreading {
		return true
	}
	if len(p.pushedBack) > 0 {
		return true
	}
	if pendingBytes >= maxPendingApplyBytes || p.height-applyHeight >= maxOutstandingHeights {
		return false
	}
	return p.height <= p.peerStore.MaxHeight()
}
