package blocksync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	mrand "math/rand"
	"sort"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/p2p"
	clientmocks "github.com/dashpay/tenderdash/internal/p2p/client/mocks"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/state/mocks"
	statefactory "github.com/dashpay/tenderdash/internal/state/test/factory"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/promise"
	tmrand "github.com/dashpay/tenderdash/libs/rand"
	"github.com/dashpay/tenderdash/libs/workerpool"
	"github.com/dashpay/tenderdash/proto/tendermint/blocksync"
	"github.com/dashpay/tenderdash/types"
	"github.com/dashpay/tenderdash/version"
)

type SynchronizerTestSuite struct {
	suite.Suite

	store        *mocks.BlockStore
	blockExec    *mocks.Executor
	client       *clientmocks.BlockClient
	responses    []*blocksync.BlockResponse
	initialState sm.State
}

func TestSynchronizer(t *testing.T) {
	suite.Run(t, new(SynchronizerTestSuite))
}

func (suite *SynchronizerTestSuite) SetupSuite() {
	ctx := context.Background()
	const chainLen = 200
	valSet, privVals := factory.MockValidatorSet()
	suite.initialState = fakeInitialState(valSet)
	state := suite.initialState.Copy()
	blocks := statefactory.MakeBlocks(ctx, suite.T(), chainLen+1, &state, privVals, 1)
	suite.responses = generateBlockResponses(suite.T(), blocks)
}

func (suite *SynchronizerTestSuite) SetupTest() {
	suite.client = clientmocks.NewBlockClient(suite.T())
	suite.store = mocks.NewBlockStore(suite.T())
	suite.blockExec = mocks.NewExecutor(suite.T())
}

func (suite *SynchronizerTestSuite) TestBasic() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startAt := int64(42)
	peers := makePeers(10, startAt, 200)

	suite.store.
		On("SaveBlock", mock.Anything, mock.Anything, mock.Anything).
		Maybe()
	suite.blockExec.
		On("ValidateBlock", mock.Anything, mock.Anything, mock.Anything).
		Maybe().
		Return(nil)
	suite.blockExec.
		On("ApplyBlock", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Maybe().
		Return(func(_ context.Context, state sm.State, _ types.BlockID, block *types.Block, _ *types.Commit) sm.State {
			return state
		}, nil)
	suite.client.
		On("GetBlock", mock.Anything, mock.Anything, mock.Anything).
		Maybe().
		Return(func(ctx context.Context, height int64, peerID types.NodeID) *promise.Promise[*blocksync.BlockResponse] {
			return promise.New(func(resolve func(data *blocksync.BlockResponse), reject func(err error)) {
				resolve(suite.responses[int(height-1)])
			})
		}, nil)

	applier := newBlockApplier(suite.blockExec, suite.store, applierWithState(suite.initialState))
	sync := NewSynchronizer(startAt, suite.client, applier)

	if err := sync.Start(ctx); err != nil {
		suite.Require().Error(err)
	}

	// Introduce each peer.
	for _, peer := range peers {
		sync.AddPeer(newPeerData(peer.peerID, peer.base, peer.height))
	}
	suite.Require().Eventually(func() bool {
		return !sync.IsCaughtUp()
	}, 2*time.Second, 10*time.Millisecond)
	sync.Stop()
}

func (suite *SynchronizerTestSuite) TestProduceJob() {
	ctx := context.Background()
	peer1 := newPeerData("peer1", 1, 1000)
	testCases := []struct {
		startHeight  int64
		wantHeight   int64
		pushBack     []int64
		wantPeer     PeerData
		isJobChEmpty bool
	}{
		{
			startHeight: 1,
			wantHeight:  1,
			wantPeer:    peer1,
		},
		{
			startHeight: 2,
			wantHeight:  2,
			wantPeer:    peer1,
		},
		{
			startHeight: 2,
			pushBack:    []int64{1},
			wantHeight:  1,
			wantPeer:    peer1,
		},
		{
			startHeight:  1001,
			isJobChEmpty: true,
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("%d", i), func() {
			applier := newBlockApplier(suite.blockExec, suite.store, applierWithState(suite.initialState))
			jobCh := make(chan *workerpool.Job, 1)
			wp := workerpool.New(0, workerpool.WithJobCh(jobCh))
			pool := NewSynchronizer(tc.startHeight, suite.client, applier, WithWorkerPool(wp))
			pool.AddPeer(peer1)
			for _, height := range tc.pushBack {
				pool.jobGen.pushBack(height)
			}
			pool.produceJob(ctx)
			if tc.isJobChEmpty {
				suite.Require().Len(jobCh, 0)
				return
			}
			suite.Require().Len(jobCh, 1)
			job := <-jobCh
			suite.client.
				On("GetBlock", mock.Anything, tc.wantHeight, tc.wantPeer.peerID).
				Once().
				Return(nil, errors.New("error"))
			_ = job.Execute(ctx)
		})
	}
}

func (suite *SynchronizerTestSuite) TestConsumeJobResult() {
	ctx := context.Background()

	resultCh := make(chan workerpool.Result, 1)
	wp := workerpool.New(1, workerpool.WithResultCh(resultCh))
	mockErr := &errBlockFetch{peerID: "peer 1", height: 1, err: errors.New("error")}
	peerID1 := types.NodeID("peer 1")
	peerID2 := types.NodeID("peer 2")
	respH1, _ := BlockResponseFromProto(suite.responses[0], peerID1)
	respH2, _ := BlockResponseFromProto(suite.responses[1], peerID1)
	respH3, _ := BlockResponseFromProto(suite.responses[2], peerID2)
	testCases := []struct {
		result       workerpool.Result
		mockFn       func(pool *Synchronizer)
		wantPushBack []int64
	}{
		{
			result: workerpool.Result{Value: respH1},
			mockFn: func(pool *Synchronizer) {
				suite.store.
					On("SaveBlock", mock.Anything, mock.Anything, mock.Anything).
					Once().
					Return(nil)
				suite.blockExec.
					On("ValidateBlock", mock.Anything, mock.Anything, respH1.Block).
					Once().
					Return(nil)
				suite.blockExec.
					On("ApplyBlock", mock.Anything, mock.Anything, mock.Anything, respH1.Block, respH1.Commit).
					Once().
					Return(sm.State{}, nil)
			},
		},
		{
			result:       workerpool.Result{Err: mockErr},
			wantPushBack: []int64{1},
			mockFn: func(pool *Synchronizer) {
				suite.client.
					On("Send", mock.Anything, p2p.PeerError{NodeID: "peer 1", Err: mockErr}).
					Once().
					Return(nil)
			},
		},
		{
			result:       workerpool.Result{Err: mockErr},
			wantPushBack: []int64{1, 2},
			mockFn: func(pool *Synchronizer) {
				pool.pendingToApply[2] = *respH2
				pool.pendingToApply[3] = *respH3
				suite.client.
					On("Send", mock.Anything, p2p.PeerError{NodeID: "peer 1", Err: mockErr}).
					Once().
					Return(nil)
			},
		},
		{
			result:       workerpool.Result{Value: respH1},
			wantPushBack: []int64{1, 2},
			mockFn: func(pool *Synchronizer) {
				pool.pendingToApply[2] = BlockResponse{PeerID: "peer 1", Block: respH2.Block}
				suite.blockExec.
					On("ValidateBlock", mock.Anything, mock.Anything, respH1.Block).
					Once().
					Return(errors.New("invalid error"))
				suite.client.
					On("Send", mock.Anything, mock.Anything).
					Once().
					Return(nil)
			},
		},
		{
			result: workerpool.Result{Value: respH2},
			mockFn: func(pool *Synchronizer) {},
		},
	}
	for i, tc := range testCases {
		applier := newBlockApplier(suite.blockExec, suite.store, applierWithState(suite.initialState))
		pool := NewSynchronizer(1, suite.client, applier, WithWorkerPool(wp))
		suite.Run(fmt.Sprintf("%d", i), func() {
			tc.mockFn(pool)
			resultCh <- tc.result
			pool.consumeJobResult(ctx)
			sort.Slice(pool.jobGen.pushedBack, func(i, j int) bool {
				return pool.jobGen.pushedBack[i] < pool.jobGen.pushedBack[j]
			})
			suite.Require().Equal(tc.wantPushBack, pool.jobGen.pushedBack)
			suite.Require().Equal(int32(-1), pool.jobProgressCounter.Load())
		})
	}
}

func (suite *SynchronizerTestSuite) TestRemovePeer() {
	peerID1 := types.NodeID("peer1")
	peerID2 := types.NodeID("peer2")
	peerID3 := types.NodeID("peer3")
	const maxHeight = 300
	peers := []PeerData{
		newPeerData(peerID1, 1, 100),
		newPeerData(peerID2, 1, 200),
		newPeerData(peerID3, 1, maxHeight),
	}
	respH1, _ := BlockResponseFromProto(suite.responses[0], peerID1)
	respH2, _ := BlockResponseFromProto(suite.responses[1], peerID2)
	respH3, _ := BlockResponseFromProto(suite.responses[2], peerID3)
	respH4, _ := BlockResponseFromProto(suite.responses[3], peerID1)
	respH5, _ := BlockResponseFromProto(suite.responses[4], peerID1)
	respH6, _ := BlockResponseFromProto(suite.responses[5], peerID3)
	respH7, _ := BlockResponseFromProto(suite.responses[6], peerID2)
	responses := []*BlockResponse{respH1, respH2, respH3, respH4, respH5, respH6, respH7}
	testCases := []struct {
		peers           []PeerData
		responses       []*BlockResponse
		peerID          types.NodeID
		initialPushBack []int64
		wantPushBack    []int64
		wantPending     []int64
		wantPeers       []PeerData
		wantMaxHeight   int64
	}{
		{
			peers:         peers,
			responses:     responses,
			peerID:        peerID1,
			wantPushBack:  []int64{1, 4, 5},
			wantPending:   []int64{2, 3, 6, 7},
			wantPeers:     []PeerData{peers[1], peers[2]},
			wantMaxHeight: maxHeight,
		},
		{
			// Heights queued for re-fetch before the removal are not queued twice.
			peers:           peers,
			responses:       responses,
			peerID:          peerID1,
			initialPushBack: []int64{4},
			wantPushBack:    []int64{1, 4, 5},
			wantPending:     []int64{2, 3, 6, 7},
			wantPeers:       []PeerData{peers[1], peers[2]},
			wantMaxHeight:   maxHeight,
		},
		{
			peers:         peers,
			responses:     responses,
			peerID:        peerID2,
			wantPushBack:  []int64{2, 7},
			wantPending:   []int64{1, 3, 4, 5, 6},
			wantPeers:     []PeerData{peers[0], peers[2]},
			wantMaxHeight: maxHeight,
		},
		{
			peers:         peers,
			responses:     responses,
			peerID:        peerID3,
			wantPushBack:  []int64{3, 6},
			wantPending:   []int64{1, 2, 4, 5, 7},
			wantPeers:     []PeerData{peers[0], peers[1]},
			wantMaxHeight: 200,
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("%d", i), func() {
			applier := newBlockApplier(suite.blockExec, suite.store, applierWithState(suite.initialState))
			pool := NewSynchronizer(1, suite.client, applier)
			for _, peer := range peers {
				pool.AddPeer(peer)
			}
			for _, resp := range tc.responses {
				pool.pendingToApply[resp.Block.Height] = *resp
			}
			for _, height := range tc.initialPushBack {
				pool.jobGen.pushBack(height)
			}
			pool.RemovePeer(tc.peerID)
			sort.Slice(pool.jobGen.pushedBack, func(i, j int) bool {
				return pool.jobGen.pushedBack[i] < pool.jobGen.pushedBack[j]
			})
			suite.Require().Equal(tc.wantPushBack, pool.jobGen.pushedBack)
			// Re-requested heights must not stay pending, otherwise the re-fetched
			// block is rejected as a duplicate.
			pending := make([]int64, 0, len(pool.pendingToApply))
			for height := range pool.pendingToApply {
				pending = append(pending, height)
			}
			sort.Slice(pending, func(i, j int) bool { return pending[i] < pending[j] })
			suite.Require().Equal(tc.wantPending, pending)
			actualPeers := pool.peerStore.All()
			suite.Require().Equal(tc.wantPeers, actualPeers)
			suite.Require().Equal(tc.wantMaxHeight, pool.MaxPeerHeight())
		})
	}
}

// TestConsumeDuplicateBlock checks that a block response for a height that is
// already pending is dropped without reporting the peer that served it. The peer
// answered a request we sent, so blaming it for the duplicate evicts good peers.
func (suite *SynchronizerTestSuite) TestConsumeDuplicateBlock() {
	ctx := context.Background()

	peerID1 := types.NodeID("peer 1")
	peerID2 := types.NodeID("peer 2")
	respH1, _ := BlockResponseFromProto(suite.responses[0], peerID1)
	duplicateH1, _ := BlockResponseFromProto(suite.responses[0], peerID2)

	resultCh := make(chan workerpool.Result, 1)
	wp := workerpool.New(1, workerpool.WithResultCh(resultCh))
	applier := newBlockApplier(suite.blockExec, suite.store, applierWithState(suite.initialState))
	// start at height 2 so that nothing is applied and the duplicate is all that
	// is under test
	pool := NewSynchronizer(2, suite.client, applier, WithWorkerPool(wp))
	pool.pendingToApply[respH1.Block.Height] = *respH1

	resultCh <- workerpool.Result{Value: duplicateH1}
	suite.Require().NoError(pool.consumeJobResult(ctx))

	// The response we already had is kept, and no peer error was sent. The client
	// mock has no Send expectation, so a report would fail this test.
	suite.Require().Equal(peerID1, pool.pendingToApply[respH1.Block.Height].PeerID)
	suite.client.AssertNotCalled(suite.T(), "Send", mock.Anything, mock.Anything)
}

// TestDuplicateBlockIsVisibleAtDefaultLogLevel checks that dropping a duplicate
// response is reported at the default log level. A duplicate means we requested a
// height twice, and that re-request cascade is what an operator has to be able to
// see; the package exposes no metric to see it by.
func (suite *SynchronizerTestSuite) TestDuplicateBlockIsVisibleAtDefaultLogLevel() {
	ctx := context.Background()

	var logs bytes.Buffer
	logger, err := log.NewLogger(config.DefaultLogLevel, &logs)
	suite.Require().NoError(err)

	peerID1 := types.NodeID("peer 1")
	peerID2 := types.NodeID("peer 2")
	respH1, _ := BlockResponseFromProto(suite.responses[0], peerID1)
	duplicateH1, _ := BlockResponseFromProto(suite.responses[0], peerID2)

	resultCh := make(chan workerpool.Result, 1)
	wp := workerpool.New(1, workerpool.WithResultCh(resultCh))
	applier := newBlockApplier(suite.blockExec, suite.store, applierWithState(suite.initialState))
	// Start above the duplicated height so nothing is applied and the duplicate is
	// all that is under test.
	pool := NewSynchronizer(2, suite.client, applier, WithWorkerPool(wp), WithLogger(logger))
	pool.pendingToApply[respH1.Block.Height] = *respH1

	resultCh <- workerpool.Result{Value: duplicateH1}
	suite.Require().NoError(pool.consumeJobResult(ctx))

	suite.Require().Contains(logs.String(), "dropping duplicate block response")
	suite.Require().Contains(logs.String(), string(peerID2))
}

// TestApplyFailurePunishesSupplyingPeer checks that an apply failure is charged to
// the peer that supplied the failing block, not to the peer whose response merely
// happened to be consumed last. Otherwise a single peer can poison a height and
// evict every honest peer answering after it, while never advancing the height.
func (suite *SynchronizerTestSuite) TestApplyFailurePunishesSupplyingPeer() {
	ctx := context.Background()

	poisonPeerID := types.NodeID("poison peer")
	honestPeerID := types.NodeID("honest peer")
	poisonH1, _ := BlockResponseFromProto(suite.responses[0], poisonPeerID)
	honestH2, _ := BlockResponseFromProto(suite.responses[1], honestPeerID)

	resultCh := make(chan workerpool.Result, 1)
	wp := workerpool.New(1, workerpool.WithResultCh(resultCh))
	applier := newBlockApplier(suite.blockExec, suite.store, applierWithState(suite.initialState))
	pool := NewSynchronizer(1, suite.client, applier, WithWorkerPool(wp))
	pool.AddPeer(newPeerData(poisonPeerID, 1, 100))
	pool.AddPeer(newPeerData(honestPeerID, 1, 100))
	pool.pendingToApply[poisonH1.Block.Height] = *poisonH1

	suite.blockExec.
		On("ValidateBlock", mock.Anything, mock.Anything, poisonH1.Block).
		Once().
		Return(errors.New("invalid block"))
	suite.client.
		On("Send", mock.Anything, mock.MatchedBy(func(peerErr p2p.PeerError) bool {
			return peerErr.NodeID == poisonPeerID
		})).
		Once().
		Return(nil)

	resultCh <- workerpool.Result{Value: honestH2}
	suite.Require().NoError(pool.consumeJobResult(ctx))

	// The poisoned height is dropped and re-requested from someone else, so the
	// synchronizer can still advance.
	suite.Require().Equal([]int64{poisonH1.Block.Height}, pool.jobGen.pushedBack)
	suite.Require().NotContains(pool.pendingToApply, poisonH1.Block.Height)
	_, found := pool.peerStore.Get(poisonPeerID)
	suite.Require().False(found)

	// The honest peer keeps both its response and its place in the peer store.
	suite.Require().Contains(pool.pendingToApply, honestH2.Block.Height)
	_, found = pool.peerStore.Get(honestPeerID)
	suite.Require().True(found)
}

// TestAddBlockDropsAlreadyAppliedHeight checks that a straggler response for an
// already applied height is dropped instead of stored. Only the entry at the
// current height is ever read, so an entry below it occupies pendingToApply
// forever, and the peer that answered our own late request must not be punished.
func (suite *SynchronizerTestSuite) TestAddBlockDropsAlreadyAppliedHeight() {
	ctx := context.Background()

	const currentHeight = int64(5)
	peerID := types.NodeID("peer 1")
	staleH1, _ := BlockResponseFromProto(suite.responses[0], peerID)
	suite.Require().Less(staleH1.Block.Height, currentHeight)

	resultCh := make(chan workerpool.Result, 1)
	wp := workerpool.New(1, workerpool.WithResultCh(resultCh))
	applier := newBlockApplier(suite.blockExec, suite.store, applierWithState(suite.initialState))
	pool := NewSynchronizer(currentHeight, suite.client, applier, WithWorkerPool(wp))

	resultCh <- workerpool.Result{Value: staleH1}
	suite.Require().NoError(pool.consumeJobResult(ctx))

	suite.Require().Empty(pool.pendingToApply)
	suite.client.AssertNotCalled(suite.T(), "Send", mock.Anything, mock.Anything)
}

// TestRemovePeerReleasesLockBeforePushBack checks that removing a peer does not
// hold the synchronizer lock while re-queueing heights on the job generator.
// Holding both couples every status read to the job generator lock, and peer
// removals are triggered by connection churn.
func (suite *SynchronizerTestSuite) TestRemovePeerReleasesLockBeforePushBack() {
	peerID := types.NodeID("peer 1")
	respH1, _ := BlockResponseFromProto(suite.responses[0], peerID)

	applier := newBlockApplier(suite.blockExec, suite.store, applierWithState(suite.initialState))
	pool := NewSynchronizer(1, suite.client, applier)
	pool.AddPeer(newPeerData(peerID, 1, 100))
	pool.pendingToApply[respH1.Block.Height] = *respH1

	// Block the job generator so the re-queue cannot complete.
	pool.jobGen.mtx.Lock()
	removed := make(chan struct{})
	go func() {
		defer close(removed)
		pool.RemovePeer(peerID)
	}()
	// Give the removal time to reach the re-queue and stall there.
	time.Sleep(50 * time.Millisecond)

	status := make(chan struct{})
	go func() {
		defer close(status)
		pool.GetStatus()
	}()
	select {
	case <-status:
	case <-time.After(2 * time.Second):
		suite.Require().Fail("GetStatus blocked on the job generator lock held elsewhere")
	}

	pool.jobGen.mtx.Unlock()
	select {
	case <-removed:
	case <-time.After(2 * time.Second):
		suite.Require().Fail("RemovePeer did not finish after the job generator lock was released")
	}
	suite.Require().Equal([]int64{respH1.Block.Height}, pool.jobGen.pushedBack)
}

func (suite *SynchronizerTestSuite) TestUpdateMonitor() {
	testCases := []struct {
		name     string
		interval int64
		options  []OptionFunc
		advance  time.Duration
		expected float64
	}{
		{
			name:     "default interval",
			interval: defaultSyncRateIntervalBlocks,
			options:  nil,
			advance:  10 * time.Millisecond,
			expected: 100,
		},
		{
			name:     "custom interval",
			interval: 50,
			options:  []OptionFunc{WithMonitorInterval(50)},
			advance:  20 * time.Millisecond,
			expected: 50,
		},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			fakeClock := clockwork.NewFakeClock()
			applier := newBlockApplier(suite.blockExec, suite.store, applierWithState(suite.initialState))
			opts := append([]OptionFunc{WithClock(fakeClock)}, tc.options...)
			sync := NewSynchronizer(1, suite.client, applier, opts...)
			suite.Require().Equal(tc.interval, sync.monitorInterval)
			sync.lastMonitorUpdate = fakeClock.Now()
			for i := int64(1); i <= tc.interval; i++ {
				sync.height++
				fakeClock.Advance(tc.advance)
				sync.updateMonitor()
				if i < tc.interval {
					suite.Require().Zero(sync.lastSyncRate)
				} else {
					suite.Require().InDelta(tc.expected, sync.lastSyncRate, 1e-9)
				}
			}
		})
	}
}

// TestStopReleasesHandlers locks down the switch-to-consensus path: after Stop()
// the producer/consumer goroutines must exit even when the parent context passed
// to Start is still live and the job generator is caught up (so produceJob keeps
// idling without ever calling Send). A leaked producer goroutine + idle timer
// would otherwise survive for the whole consensus phase.
func (suite *SynchronizerTestSuite) TestStopReleasesHandlers() {
	// Parent context stays live for the whole test — Stop must not depend on it.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	applier := newBlockApplier(suite.blockExec, suite.store, applierWithState(suite.initialState))
	// startHeight 1 with no peers => MaxHeight 0 => shouldJobBeGenerated() is false,
	// so produceJob idles (sleep + return nil) and never observes ErrWorkerPoolStopped.
	sync := NewSynchronizer(1, suite.client, applier)

	// Snapshot the goroutine baseline now and defer the leak check so it still runs
	// (and fails the test) if an assertion below trips mid-way.
	defer leaktest.CheckTimeout(suite.T(), 5*time.Second)()

	suite.Require().NoError(sync.Start(ctx))
	suite.Require().Eventually(sync.IsRunning, time.Second, 5*time.Millisecond)
	// Give the idling producer a few iterations before stopping.
	time.Sleep(50 * time.Millisecond)

	sync.Stop()

	// With the parent ctx still live, both handler goroutines must have exited.
	suite.Require().NoError(ctx.Err())
}

// TestProduceJobFailureKeepsCounterBalanced locks down the in-progress counter
// accounting in produceJob: the increment happens before Send, so every path that
// fails to hand the job to a worker must undo it. Otherwise GetStatus's in-progress
// count leaks upward permanently. We force each failure path and assert the count
// reported by GetStatus returns to its baseline.
func (suite *SynchronizerTestSuite) TestProduceJobFailureKeepsCounterBalanced() {
	const startAt = int64(10)

	testCases := []struct {
		name    string
		wantErr error
		// prepare returns a context for produceJob; the synchronizer already has a
		// peer so shouldJobBeGenerated() is true and nextJob can find a peer.
		prepare func(sync *Synchronizer) context.Context
	}{
		{
			// nextJob -> getPeer aborts on the canceled context, hitting the
			// nextJob error path in produceJob.
			name:    "nextJob error",
			wantErr: context.Canceled,
			prepare: func(_ *Synchronizer) context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
		},
		{
			// A stopped pool makes Send return ErrWorkerPoolStopped after the
			// increment, hitting the Send error path in produceJob.
			name:    "send to stopped pool",
			wantErr: workerpool.ErrWorkerPoolStopped,
			prepare: func(sync *Synchronizer) context.Context {
				sync.workerPool.Stop(context.Background())
				return context.Background()
			},
		},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			pool := workerpool.New(1)
			applier := newBlockApplier(suite.blockExec, suite.store, applierWithState(suite.initialState))
			sync := NewSynchronizer(startAt, suite.client, applier, WithWorkerPool(pool))

			peerID := types.NodeID(tmrand.Str(12))
			sync.AddPeer(newPeerData(peerID, startAt, startAt+100))

			suite.Require().True(sync.jobGen.shouldJobBeGenerated())
			_, baseline := sync.GetStatus()

			ctx := tc.prepare(sync)

			err := sync.produceJob(ctx)
			suite.Require().ErrorIs(err, tc.wantErr)

			_, after := sync.GetStatus()
			suite.Require().Equal(baseline, after, "in-progress counter must return to baseline after a failed produceJob")
		})
	}
}

func generateBlockResponses(t *testing.T, blocks []*types.Block) []*blocksync.BlockResponse {
	responses := make([]*blocksync.BlockResponse, 0, len(blocks)-1)
	for i := 0; i < len(blocks)-1; i++ {
		protoBlock, err := blocks[i].ToProto()
		require.NoError(t, err)
		responses = append(responses, &blocksync.BlockResponse{
			Block:  protoBlock,
			Commit: blocks[i+1].LastCommit.ToProto(),
		})
	}
	return responses
}

func fakeInitialState(valSet *types.ValidatorSet) sm.State {
	return sm.State{
		Version: sm.Version{
			Consensus: version.Consensus{
				Block: version.BlockProtocol,
			},
		},
		ChainID:        "test-chain",
		InitialHeight:  1,
		Validators:     valSet,
		LastValidators: valSet,
	}
}

func makePeers(numPeers int, minHeight, maxHeight int64) map[types.NodeID]PeerData {
	peers := make(map[types.NodeID]PeerData, numPeers)
	for i := 0; i < numPeers; i++ {
		peerID := types.NodeID(tmrand.Str(12))
		height := minHeight + mrand.Int63n(maxHeight-minHeight)
		base := minHeight + int64(i)
		if base > height {
			base = height
		}
		peers[peerID] = newPeerData(peerID, base, height)
	}
	return peers
}
