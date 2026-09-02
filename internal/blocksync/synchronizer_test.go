package blocksync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	mrand "math/rand"
	"runtime"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/libs/flowrate"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/client"
	clientmocks "github.com/dashpay/tenderdash/internal/p2p/client/mocks"
	p2pmocks "github.com/dashpay/tenderdash/internal/p2p/mocks"
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
	// backlogResponses is a chain long enough to run past the pending window,
	// built on first use - see backlogChain.
	backlogResponses []*blocksync.BlockResponse
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
	expectApply(suite.blockExec, func(call *mock.Call) { call.Maybe() })
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
	// the same height answered by a second peer, i.e. the duplicate a retry race
	// produces
	duplicateH2, _ := BlockResponseFromProto(suite.responses[1], peerID2)
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
					On("ProcessProposal", mock.Anything, respH1.Block, mock.Anything, mock.Anything, false).
					Once().
					Return(sm.CurrentRoundState{}, nil)
				suite.blockExec.
					On("FinalizeBlock", mock.Anything, mock.Anything, mock.Anything, mock.Anything, respH1.Block, respH1.Commit).
					Once().
					Return(sm.State{}, nil)
			},
		},
		{
			// a lone failure re-requests the height but leaves the peer alone, so
			// the client mock deliberately has no Send expectation here
			result:       workerpool.Result{Err: mockErr},
			wantPushBack: []int64{1},
			mockFn:       func(_ *Synchronizer) {},
		},
		{
			// the peer starts one failure short of the threshold, so this result
			// crosses it: the peer is reported and its pending blocks re-requested
			result:       workerpool.Result{Err: mockErr},
			wantPushBack: []int64{1, 2},
			mockFn: func(pool *Synchronizer) {
				pool.AddPeer(newPeerData(peerID1, 1, 100))
				for i := int32(1); i < maxConsecutiveFailures; i++ {
					pool.peerStore.AddFailure(peerID1, maxConsecutiveFailures)
				}
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
		{
			// The case this PR is about: height 2 was requested from two peers, the
			// first answer is waiting on height 1 to arrive, and the second answer
			// now shows up. It must be dropped without reporting its sender, and
			// without re-queueing the height. suite.client has no Send expectation,
			// so a peer report fails this case.
			result: workerpool.Result{Value: duplicateH2},
			mockFn: func(pool *Synchronizer) {
				pool.pendingToApply[respH2.Block.Height] = *respH2
			},
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

// TestConsumeDuplicateThenDrain covers what the TestConsumeJobResult table cannot,
// because it needs two results in sequence: that dropping a duplicate leaves the
// synchronizer able to make progress afterwards.
//
// Height 2 is answered by two peers while height 1 is still outstanding. The
// duplicate is dropped without reporting its sender, and once height 1 arrives
// both blocks apply - which exercises the fall-through from the duplicate branch
// into applyBlock, not just the drop itself.
func (suite *SynchronizerTestSuite) TestConsumeDuplicateThenDrain() {
	ctx := context.Background()

	peerID1 := types.NodeID("peer 1")
	peerID2 := types.NodeID("peer 2")
	respH1, _ := BlockResponseFromProto(suite.responses[0], peerID1)
	respH2, _ := BlockResponseFromProto(suite.responses[1], peerID1)
	duplicateH2, _ := BlockResponseFromProto(suite.responses[1], peerID2)

	resultCh := make(chan workerpool.Result, 2)
	wp := workerpool.New(1, workerpool.WithResultCh(resultCh))
	applier := newBlockApplier(suite.blockExec, suite.store, applierWithState(suite.initialState))
	pool := NewSynchronizer(1, suite.client, applier, WithWorkerPool(wp))
	// height 2 is already in hand and waiting on height 1
	pool.pendingToApply[respH2.Block.Height] = *respH2

	resultCh <- workerpool.Result{Value: duplicateH2}
	suite.Require().NoError(pool.consumeJobResult(ctx))
	suite.Require().Equal(peerID1, pool.pendingToApply[respH2.Block.Height].PeerID,
		"the response already held must be kept")
	suite.Require().Empty(pool.jobGen.pushedBack, "a duplicate must not re-queue the height")
	suite.client.AssertNotCalled(suite.T(), "Send", mock.Anything, mock.Anything)

	suite.store.
		On("SaveBlock", mock.Anything, mock.Anything, mock.Anything).
		Twice()
	suite.blockExec.
		On("ValidateBlock", mock.Anything, mock.Anything, mock.Anything).
		Twice().
		Return(nil)
	expectApply(suite.blockExec, func(call *mock.Call) { call.Twice() })

	resultCh <- workerpool.Result{Value: respH1}
	suite.Require().NoError(pool.consumeJobResult(ctx))

	suite.Require().Empty(pool.pendingToApply, "both blocks must have been applied")
	height, _ := pool.GetStatus()
	suite.Require().EqualValues(3, height, "the synchronizer must have advanced past both blocks")
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

			suite.Require().True(sync.jobGen.shouldJobBeGenerated(startAt, 0))
			_, baseline := sync.GetStatus()

			ctx := tc.prepare(sync)

			err := sync.produceJob(ctx)
			suite.Require().ErrorIs(err, tc.wantErr)

			_, after := sync.GetStatus()
			suite.Require().Equal(baseline, after, "in-progress counter must return to baseline after a failed produceJob")
		})
	}
}

// TestStatusRefreshPreservesFailureCount checks that a status response does not
// restart a peer's run of consecutive failures. Peers refresh their status every
// few seconds while the block request timeout is far longer, so a failing peer
// would otherwise be handed a clean slate between every two failures and the
// threshold would never be reached.
func (suite *SynchronizerTestSuite) TestStatusRefreshPreservesFailureCount() {
	ctx := context.Background()

	peerID := types.NodeID("peer 1")
	resultCh := make(chan workerpool.Result, 1)
	wp := workerpool.New(1, workerpool.WithResultCh(resultCh))
	applier := newBlockApplier(suite.blockExec, suite.store, applierWithState(suite.initialState))
	pool := NewSynchronizer(1, suite.client, applier, WithWorkerPool(wp))
	pool.AddPeer(newPeerData(peerID, 1, 100))

	failRequest := func(height int64) {
		resultCh <- workerpool.Result{
			Err: &errBlockFetch{peerID: peerID, height: height, err: errors.New("timeout")},
		}
		suite.Require().NoError(pool.consumeJobResult(ctx))
	}

	// stop one short of the threshold, then let a status response land mid-run
	for i := int32(1); i < maxConsecutiveFailures; i++ {
		failRequest(int64(i))
	}
	pool.AddPeer(newPeerData(peerID, 1, 200))
	suite.Require().Len(pool.peerStore.All(), 1, "the peer must still be known after a status refresh")

	// the next failure completes the run and must report the peer
	suite.client.
		On("Send", mock.Anything, mock.Anything).
		Once().
		Return(nil)
	failRequest(99)

	suite.Require().Empty(pool.peerStore.All(),
		"a status refresh must not restart the run of consecutive failures")
}

// TestStatusRefreshPreservesPendingRequests checks that a status response leaves
// the count of requests already in flight alone. Losing it lets the peer be picked
// past the per-peer request limit, and the completions of those forgotten requests
// then drive the count below zero.
func (suite *SynchronizerTestSuite) TestStatusRefreshPreservesPendingRequests() {
	ctx := context.Background()

	const inFlight = 3
	peerID := types.NodeID("peer 1")
	jobCh := make(chan *workerpool.Job, inFlight)
	resultCh := make(chan workerpool.Result, 1)
	wp := workerpool.New(0, workerpool.WithJobCh(jobCh), workerpool.WithResultCh(resultCh))
	applier := newBlockApplier(suite.blockExec, suite.store, applierWithState(suite.initialState))
	pool := NewSynchronizer(1, suite.client, applier, WithWorkerPool(wp))
	pool.AddPeer(newPeerData(peerID, 1, 100))

	numPending := func() int32 {
		peer, found := pool.peerStore.Get(peerID)
		suite.Require().True(found)
		return peer.numPending
	}

	for i := 0; i < inFlight; i++ {
		suite.Require().NoError(pool.produceJob(ctx))
	}
	suite.Require().EqualValues(inFlight, numPending())

	pool.AddPeer(newPeerData(peerID, 1, 200))
	suite.Require().EqualValues(inFlight, numPending(),
		"requests already in flight must survive a status refresh")

	// the peer stays below the failure threshold, so the client mock has no Send
	// expectation and reporting the peer would fail this test
	for i := 0; i < inFlight; i++ {
		resultCh <- workerpool.Result{
			Err: &errBlockFetch{peerID: peerID, height: int64(i + 1), err: errors.New("timeout")},
		}
		suite.Require().NoError(pool.consumeJobResult(ctx))
		suite.Require().GreaterOrEqual(numPending(), int32(0),
			"the pending request count must never go negative")
	}
	suite.Require().EqualValues(0, numPending())
}

// TestStatusRefreshUpdatesAdvertisedRange checks that a status response still does
// the one thing it is for: moving the range of blocks the peer claims to serve. It
// is the guard on the tests above, which a synchronizer that ignored status
// responses outright would also pass.
func (suite *SynchronizerTestSuite) TestStatusRefreshUpdatesAdvertisedRange() {
	peerID1 := types.NodeID("peer 1")
	peerID2 := types.NodeID("peer 2")

	applier := newBlockApplier(suite.blockExec, suite.store, applierWithState(suite.initialState))
	pool := NewSynchronizer(1, suite.client, applier)
	pool.AddPeer(newPeerData(peerID1, 1, 100))
	pool.AddPeer(newPeerData(peerID2, 1, 50))
	suite.Require().EqualValues(100, pool.MaxPeerHeight())

	pool.AddPeer(newPeerData(peerID1, 10, 200))
	peer, found := pool.peerStore.Get(peerID1)
	suite.Require().True(found)
	suite.Require().EqualValues(10, peer.base)
	suite.Require().EqualValues(200, peer.height)
	suite.Require().EqualValues(200, pool.MaxPeerHeight())

	// a peer whose blocks were pruned reports a higher base and a lower height; the
	// highest block we believe is reachable must not stay at a height no peer has,
	// or the synchronizer never considers itself caught up
	pool.AddPeer(newPeerData(peerID1, 60, 70))
	peer, found = pool.peerStore.Get(peerID1)
	suite.Require().True(found)
	suite.Require().EqualValues(60, peer.base)
	suite.Require().EqualValues(70, peer.height)
	suite.Require().EqualValues(70, pool.MaxPeerHeight())
}

// TestStatusRefreshKeepsSlowPeerEvictable checks that a status response does not
// hide a peer that is answering too slowly. The check needs both the peer's
// pending requests and the receive rate measured across them, and a refresh that
// replaced either would restart the measurement every few seconds, so no peer
// could ever be measured over a long enough window to be evicted.
func (suite *SynchronizerTestSuite) TestStatusRefreshKeepsSlowPeerEvictable() {
	fakeClock := clockwork.NewFakeClock()
	flowrate.Now = func() time.Time { return fakeClock.Now() }
	defer func() { flowrate.Now = flowrate.TimeNow }()

	// 10000 bytes over 5 seconds - non-zero, and well below minRecvRate
	monitor := flowrate.New(time.Now(), time.Second, 10*time.Second)
	fakeClock.Advance(5 * time.Second)
	monitor.Update(10000)

	peerID := types.NodeID("peer 1")
	applier := newBlockApplier(suite.blockExec, suite.store, applierWithState(suite.initialState))
	pool := NewSynchronizer(1, suite.client, applier)
	pool.AddPeer(newPeerData(peerID, 1, 100))
	pool.peerStore.Update(peerID, AddNumPending(1), func(_ types.NodeID, peer *PeerData) {
		peer.recvMonitor = monitor
	})
	suite.Require().Len(pool.peerStore.FindTimedoutPeers(), 1)

	pool.AddPeer(newPeerData(peerID, 1, 200))

	timedout := pool.peerStore.FindTimedoutPeers()
	suite.Require().Len(timedout, 1, "a status refresh must not hide a peer that is too slow")
	suite.Require().Equal(peerID, timedout[0].peerID)
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

// TestStallVerdictFor checks when a lack of progress ends block sync. Handing
// over to consensus is effectively irreversible, so a stall must not end block
// sync while a peer still holds the block we are waiting for, nor while the node
// is so far behind that consensus catch-up could never close the gap.
func TestStallVerdictFor(t *testing.T) {
	testCases := []struct {
		name       string
		servable   bool
		behindBy   int64
		stalledFor time.Duration
		want       stallVerdict
	}{
		{
			name:       "progressing with the block available",
			servable:   true,
			stalledFor: syncTimeout / 2,
			want:       keepSyncing,
		},
		{
			name:       "progressing with nobody holding the block",
			servable:   false,
			stalledFor: syncTimeout / 2,
			want:       keepSyncing,
		},
		{
			name:       "stalled with nothing left to fetch",
			servable:   false,
			stalledFor: syncTimeout + time.Second,
			want:       stopNothingToFetch,
		},
		{
			name:       "stalled but a peer still holds the block",
			servable:   true,
			stalledFor: syncTimeout + time.Second,
			want:       keepSyncing,
		},
		{
			name:       "stalled just short of the wedge limit",
			servable:   true,
			stalledFor: maxSyncStall,
			want:       keepSyncing,
		},
		{
			name:       "stalled past the wedge limit within reach of the tip",
			servable:   true,
			behindBy:   maxCatchupGap,
			stalledFor: maxSyncStall + time.Second,
			want:       stopStalledTooLong,
		},
		{
			name:       "stalled past the wedge limit while far behind",
			servable:   true,
			behindBy:   maxCatchupGap + 1,
			stalledFor: maxSyncStall + time.Second,
			want:       keepSyncing,
		},
		{
			name:       "the wedge limit does not override nothing to fetch",
			servable:   false,
			stalledFor: maxSyncStall + time.Second,
			want:       stopNothingToFetch,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, stallVerdictFor(tc.servable, tc.behindBy, tc.stalledFor))
		})
	}
}

// waitForSyncHarness runs WaitForSync on its own goroutine against a fake clock.
// The synchronizer never advances a height, so the only thing that changes the
// verdict is how far the clock is moved.
type waitForSyncHarness struct {
	clock  *clockwork.FakeClock
	done   chan bool
	cancel context.CancelFunc
}

// newWaitForSyncHarness starts WaitForSync at the given height with the given
// peers already known, and returns once its ticker is registered with the fake
// clock - advancing the clock before that would move time nothing is waiting on.
func (suite *SynchronizerTestSuite) newWaitForSyncHarness(height int64, peers ...PeerData) *waitForSyncHarness {
	clock := clockwork.NewFakeClock()
	applier := newBlockApplier(suite.blockExec, suite.store, applierWithState(suite.initialState))
	sync := NewSynchronizer(height, suite.client, applier, WithClock(clock))
	// Deliberately not started: WaitForSync reads only the height, the peer store
	// and lastAdvance, and leaving the job pipeline out keeps the ticker the sole
	// waiter on the fake clock.
	sync.lastAdvance = clock.Now()
	for _, peer := range peers {
		sync.AddPeer(peer)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() {
		done <- sync.WaitForSync(ctx)
	}()
	suite.Require().NoError(clock.BlockUntilContext(ctx, 1))
	return &waitForSyncHarness{clock: clock, done: done, cancel: cancel}
}

// result waits up to timeout of real time for WaitForSync to return. ended
// reports whether it did; caughtUp is its verdict when it did.
func (h *waitForSyncHarness) result(timeout time.Duration) (caughtUp bool, ended bool) {
	select {
	case caughtUp = <-h.done:
		return caughtUp, true
	case <-time.After(timeout):
		return false, false
	}
}

// TestWaitForSyncStopsWhenNoPeerCanServeNextHeight checks that a peer which
// cannot serve the height we need does not keep block sync alive. A peer whose
// advertised base is above that height holds none of the blocks we are waiting
// for, however large the height it claims, so waiting on it means waiting for a
// block that will never arrive.
func (suite *SynchronizerTestSuite) TestWaitForSyncStopsWhenNoPeerCanServeNextHeight() {
	const height = int64(100)
	// The largest height the status handler accepts, paired with a base above the
	// next height we need: nothing this peer advertises overlaps what we want.
	peer := newPeerData("peer out of range", height+2, int64(1)<<60)
	h := suite.newWaitForSyncHarness(height, peer)
	defer h.cancel()

	h.clock.Advance(syncTimeout + time.Second)

	caughtUp, ended := h.result(2 * time.Second)
	suite.Require().True(ended, "block sync kept waiting on a peer whose blocks start above height %d", height)
	suite.Require().False(caughtUp)
}

// TestWaitForSyncKeepsGoingWhileAPeerCanServeUs is the regression guard on the
// test above: a stall is not by itself a reason to hand over. Handing over to
// consensus is one-way and consensus catch-up is far slower, so while some peer
// still holds the block we need, block sync keeps retrying.
func (suite *SynchronizerTestSuite) TestWaitForSyncKeepsGoingWhileAPeerCanServeUs() {
	const height = int64(100)
	peer := newPeerData("peer in range", 1, 1000)
	h := suite.newWaitForSyncHarness(height, peer)
	defer h.cancel()

	h.clock.Advance(syncTimeout + time.Second)

	_, ended := h.result(200 * time.Millisecond)
	suite.Require().False(ended, "block sync gave up while a peer still held height %d", height)
}

// TestWaitForSyncHandsOverAfterMaxSyncStall checks the wall-clock backstop.
// Servability is judged from what peers advertise about themselves, so a peer
// that claims our height and never answers keeps the node in block sync
// indefinitely unless a stall long enough to look like a wedge ends it. Within
// reach of the tip consensus catch-up can cover what is left, so the backstop
// applies.
func (suite *SynchronizerTestSuite) TestWaitForSyncHandsOverAfterMaxSyncStall() {
	const height = int64(100)
	peer := newPeerData("peer in range", 1, height+maxCatchupGap)
	h := suite.newWaitForSyncHarness(height, peer)
	defer h.cancel()

	h.clock.Advance(maxSyncStall + time.Second)

	caughtUp, ended := h.result(2 * time.Second)
	suite.Require().True(ended, "no progress for %s did not hand over to consensus", maxSyncStall)
	suite.Require().False(caughtUp, "handing over after a stall is not catching up")
}

// TestWaitForSyncKeepsSyncingWhileFarBehind is the counterpart: the wall-clock
// backstop must not hand over a node that is thousands of blocks behind. It
// would arrive in consensus as a validator at a height the network committed
// long ago (dashpay/tenderdash#1413), and consensus catch-up moves about one
// block per gossip cycle, so it would never reach the tip that way either.
func (suite *SynchronizerTestSuite) TestWaitForSyncKeepsSyncingWhileFarBehind() {
	const height = int64(100)
	peer := newPeerData("peer far ahead", 1, height+maxCatchupGap+1)
	h := suite.newWaitForSyncHarness(height, peer)
	defer h.cancel()

	h.clock.Advance(maxSyncStall + time.Second)

	_, ended := h.result(200 * time.Millisecond)
	suite.Require().False(ended, "block sync gave up while still %d blocks behind", maxCatchupGap+1)
}

// TestWaitForSyncStopsWhenTheOnlyPeerIsRateRejected covers the slow-peer route to
// the same lock-out. A peer below minRecvRate holds the block we want but will
// never be selected again and cannot be evicted while nothing is pending, so
// treating it as something to wait for costs the full wedge backstop with no
// attacker involved.
func (suite *SynchronizerTestSuite) TestWaitForSyncStopsWhenTheOnlyPeerIsRateRejected() {
	const height = int64(100)
	peer := newPeerData("slow peer", 1, 1000)
	peer.recvMonitor = newSlowMonitor(suite.T())
	h := suite.newWaitForSyncHarness(height, peer)
	defer h.cancel()

	h.clock.Advance(syncTimeout + time.Second)

	caughtUp, ended := h.result(2 * time.Second)
	suite.Require().True(ended, "block sync kept waiting on a peer it can no longer ask")
	suite.Require().False(caughtUp)
}

// TestStallSnapshotIsOneObservation checks that a block applied concurrently
// cannot tear the stall verdict's inputs apart. advance() stamps the height and
// the advance time together, so a snapshot must show the state either wholly
// before that or wholly after it - never a height from one side paired with a
// staleness or a servability from the other. Ending block sync is a one-way door,
// so a stop assembled from mixed readings is unrecoverable.
//
// The peer here holds startHeight+1 upwards, which makes servability flip exactly
// when the height moves, so a mixed reading shows up as a contradiction rather
// than as the same answer by luck.
func (suite *SynchronizerTestSuite) TestStallSnapshotIsOneObservation() {
	const (
		startHeight = int64(100)
		stalledFor  = syncTimeout + time.Second
		trials      = 2000
	)
	for trial := 0; trial < trials; trial++ {
		clock := clockwork.NewFakeClock()
		applier := newBlockApplier(suite.blockExec, suite.store, applierWithState(suite.initialState))
		sync := NewSynchronizer(startHeight, suite.client, applier,
			WithClock(clock), WithWorkerPool(workerpool.New(0)))
		sync.AddPeer(newPeerData("peer above us", startHeight+1, 1000))
		sync.lastAdvance = clock.Now().Add(-stalledFor)

		ready := make(chan struct{})
		applied := make(chan struct{})
		go func() {
			<-ready
			sync.advance()
			close(applied)
		}()
		runtime.Gosched()
		close(ready)
		height, stalled, servable, maxPeerHeight := sync.stallSnapshot()
		<-applied
		suite.Require().Equal(int64(1000), maxPeerHeight, "the height the gap is measured against")

		if height == startHeight {
			suite.Require().Equal(stalledFor, stalled,
				"height %d reported with the advance time stamped by a later height (trial %d)", height, trial)
			suite.Require().False(servable,
				"height %d reported as servable by a peer whose blocks start above it (trial %d)", height, trial)
			continue
		}
		suite.Require().Equal(startHeight+1, height, "height moved by more than the one applied block")
		suite.Require().Zero(stalled,
			"height %d reported with the advance time from before it was applied (trial %d)", height, trial)
		suite.Require().True(servable,
			"height %d reported as unservable by the peer that holds it (trial %d)", height, trial)
	}
}

// TestWaitForSyncStopsWhenThereIsNothingToFetch checks that a stall with no peer
// to fetch from ends block sync as soon as the stall is recognized, rather than
// sitting out the wedge backstop.
func (suite *SynchronizerTestSuite) TestWaitForSyncStopsWhenThereIsNothingToFetch() {
	h := suite.newWaitForSyncHarness(100)
	defer h.cancel()

	h.clock.Advance(syncTimeout + time.Second)

	caughtUp, ended := h.result(2 * time.Second)
	suite.Require().True(ended, "block sync kept waiting with no peer to fetch from")
	suite.Require().False(caughtUp)
}

// backlogChainLen is how many heights the backlog tests have blocks for. It is
// wider than the pending window so that a run which ignores the window keeps
// going well past it instead of stopping for want of a block to serve.
const backlogChainLen = maxOutstandingHeights + 200

// backlogChain returns block responses for heights 1..backlogChainLen of one
// chain, built on first use and reused afterwards. The backlog tests need more
// heights than the pending window is wide, and signing that many blocks is by
// far the most expensive thing in this file.
func (suite *SynchronizerTestSuite) backlogChain() []*blocksync.BlockResponse {
	if suite.backlogResponses == nil {
		ctx := context.Background()
		_, privVals := factory.MockValidatorSet()
		state := suite.initialState.Copy()
		blocks := statefactory.MakeBlocks(ctx, suite.T(), backlogChainLen+1, &state, privVals, 1)
		suite.backlogResponses = generateBlockResponses(suite.T(), blocks)
	}
	return suite.backlogResponses
}

// backlogHarness drives one synchronizer's producer and consumer by hand. The
// worker pool holds no workers, so the test runs each job itself the way a
// worker would; job production, peer selection, addBlock and applyBlock are the
// real thing, and all the test decides is which responses come back and when.
type backlogHarness struct {
	suite    *SynchronizerTestSuite
	pool     *Synchronizer
	jobCh    chan *workerpool.Job
	resultCh chan workerpool.Result
	chainLen int
}

// newBacklogHarness builds a synchronizer starting at height 1 with peers that
// hold the whole chain.
func (suite *SynchronizerTestSuite) newBacklogHarness() *backlogHarness {
	chain := suite.backlogChain()

	// Freeze the clock the receive-rate monitors read. A peer measured below
	// minRecvRate is never selected again, and the rate here would be measured
	// over however long the test itself takes, so a slow run would leave job
	// production waiting forever for a peer to become selectable.
	frozen := time.Now()
	flowrate.Now = func() time.Time { return frozen }
	suite.T().Cleanup(func() { flowrate.Now = flowrate.TimeNow })

	suite.client.
		On("GetBlock", mock.Anything, mock.Anything, mock.Anything).
		Maybe().
		Return(func(_ context.Context, height int64, _ types.NodeID) *promise.Promise[*blocksync.BlockResponse] {
			return promise.New(func(resolve func(data *blocksync.BlockResponse), reject func(err error)) {
				if height < 1 || height > int64(len(chain)) {
					reject(fmt.Errorf("height %d is past the end of the %d-block test chain", height, len(chain)))
					return
				}
				resolve(chain[height-1])
			})
		}, nil)
	suite.store.
		On("SaveBlock", mock.Anything, mock.Anything, mock.Anything).
		Maybe()
	suite.blockExec.
		On("ValidateBlock", mock.Anything, mock.Anything, mock.Anything).
		Maybe().
		Return(nil)
	expectApply(suite.blockExec, func(call *mock.Call) { call.Maybe() })

	jobCh := make(chan *workerpool.Job, 1)
	resultCh := make(chan workerpool.Result, 1)
	wp := workerpool.New(0, workerpool.WithJobCh(jobCh), workerpool.WithResultCh(resultCh))
	applier := newBlockApplier(suite.blockExec, suite.store, applierWithState(suite.initialState))
	pool := NewSynchronizer(1, suite.client, applier, WithWorkerPool(wp))
	// Enough peers to hold every height in the chain in flight at once, not just
	// a window's worth. A peer takes at most maxPendingRequestsPerPeer requests
	// and job production waits for one that can take another, so a run that got
	// past the limits under test would otherwise stop inside peer selection and
	// report nothing about what it did. They advertise far more than the chain
	// holds, so nothing here stops at the highest height a peer claims either.
	for i := 0; i < backlogChainLen/maxPendingRequestsPerPeer+2; i++ {
		pool.AddPeer(newPeerData(types.NodeID(fmt.Sprintf("backlog peer %02d", i)), 1, int64(1)<<40))
	}
	return &backlogHarness{suite: suite, pool: pool, jobCh: jobCh, resultCh: resultCh, chainLen: len(chain)}
}

// produce runs one iteration of the job producer and, when it hands a job to the
// pool, runs that job the way a worker would. It returns the response the peer
// gave, or nil when production was refused.
func (h *backlogHarness) produce(ctx context.Context) *BlockResponse {
	h.suite.Require().NoError(h.pool.produceJob(ctx))
	select {
	case job := <-h.jobCh:
		res := job.Execute(ctx)
		h.suite.Require().NoError(res.Err)
		return res.Value.(*BlockResponse)
	default:
		return nil
	}
}

// deliver hands a fetched response to the consumer, as the worker pool would.
func (h *backlogHarness) deliver(ctx context.Context, resp *BlockResponse) {
	h.resultCh <- workerpool.Result{Value: resp}
	h.suite.Require().NoError(h.pool.consumeJobResult(ctx))
}

// deliverSized delivers a response that reports the given serialized size. The
// blocks in the test chain are empty and the byte budget reads only the size
// recorded while a response was decoded, so a size is stated here rather than
// signing and shipping hundreds of megabytes of real blocks.
func (h *backlogHarness) deliverSized(ctx context.Context, resp *BlockResponse, size int) {
	resp.Size = size
	h.deliver(ctx, resp)
}

// timeout hands the consumer a failed fetch, as a request the peer never
// answered would once it ran out of time.
func (h *backlogHarness) timeout(ctx context.Context, peerID types.NodeID, height int64) {
	h.resultCh <- workerpool.Result{
		Err: &errBlockFetch{peerID: peerID, height: height, err: errors.New("peer did not respond")},
	}
	h.suite.Require().NoError(h.pool.consumeJobResult(ctx))
}

// fillWindow requests heights until production is refused, answering everything
// except blocking, whose request is left in flight as a peer that never replies
// would leave it. It returns the peer that request went to.
func (h *backlogHarness) fillWindow(ctx context.Context, blocking int64) types.NodeID {
	var blockingPeer types.NodeID
	for attempt := 0; attempt < h.chainLen; attempt++ {
		resp := h.produce(ctx)
		if resp == nil {
			return blockingPeer
		}
		if resp.Block.Height == blocking {
			blockingPeer = resp.PeerID
			continue
		}
		h.deliver(ctx, resp)
	}
	h.suite.Require().Fail("job production never stopped",
		"%d heights offered, %d responses buffered", h.chainLen, len(h.pool.pendingToApply))
	return blockingPeer
}

// TestBacklogIsBoundedWhileTheBlockingHeightIsMissing checks that responses which
// cannot be applied yet stop accumulating at the pending window instead of
// growing with however many heights are on offer.
//
// Applying in order is what normally throttles the whole pipeline: the consumer
// applies synchronously, so while apply is doing work the result channel fills,
// the workers block and the producer stalls. Withholding one height removes
// exactly that. applyBlock returns immediately, the consumer drains at network
// speed and every stage above it goes slack, while nothing else limits what is
// held in memory - a peer's in-flight count is released when its response
// arrives, not when its block is applied.
func (suite *SynchronizerTestSuite) TestBacklogIsBoundedWhileTheBlockingHeightIsMissing() {
	ctx := context.Background()
	h := suite.newBacklogHarness()
	// The height the synchronizer starts at, and the one its peers never answer.
	const blocking = int64(1)

	// Once production is refused it stays refused, because nothing here advances
	// the height, so ask a few more times to show the refusal is the bound and
	// not a hiccup.
	const refusalsToConfirm = 5
	requested, refused := 0, 0
	for attempt := 0; attempt < h.chainLen && refused < refusalsToConfirm; attempt++ {
		resp := h.produce(ctx)
		if resp == nil {
			refused++
			continue
		}
		requested++
		if resp.Block.Height == blocking {
			continue
		}
		h.deliver(ctx, resp)
	}

	suite.Require().Equal(refusalsToConfirm, refused,
		"job production never stopped: %d heights requested, %d responses buffered",
		requested, len(h.pool.pendingToApply))
	suite.Require().Equal(maxOutstandingHeights, requested,
		"heights requested ahead of the blocking one are not bounded by the window")
	suite.Require().Len(h.pool.pendingToApply, maxOutstandingHeights-1,
		"the buffered backlog is not bounded by the window")
	height, _ := h.pool.GetStatus()
	suite.Require().EqualValues(blocking, height,
		"nothing can be applied while the height everything is waiting for is missing")
}

// TestBacklogIsBoundedByBytesWhenBlocksAreLarge checks that the backlog is
// bounded by what it costs, not only by how many entries it has. A count is not
// a memory bound when one block may be tens of megabytes, so with large blocks
// the byte budget has to stop the backlog long before the height window would.
func (suite *SynchronizerTestSuite) TestBacklogIsBoundedByBytesWhenBlocksAreLarge() {
	ctx := context.Background()
	h := suite.newBacklogHarness()
	const blocking = int64(1)
	// Eight of these exhaust the budget, well inside the height window.
	const blockSize = maxPendingApplyBytes / 8
	const wantBuffered = 8

	const refusalsToConfirm = 5
	buffered, refused := 0, 0
	for attempt := 0; attempt < h.chainLen && refused < refusalsToConfirm; attempt++ {
		resp := h.produce(ctx)
		if resp == nil {
			refused++
			continue
		}
		if resp.Block.Height == blocking {
			continue
		}
		h.deliverSized(ctx, resp, blockSize)
		buffered++
	}

	suite.Require().Equal(refusalsToConfirm, refused,
		"job production never stopped: %d responses of %d bytes buffered", buffered, blockSize)
	suite.Require().Equal(wantBuffered, buffered, "the byte budget did not stop the backlog where it should")
	suite.Require().Less(buffered, maxOutstandingHeights,
		"the height window stopped this, so it says nothing about the byte budget")
	_, pendingBytes := h.pool.backlog()
	suite.Require().Equal(maxPendingApplyBytes, pendingBytes)
}

// TestBlockingHeightIsStillFetchableAtTheByteBound is the no-wedge guard for
// the byte budget, the counterpart of the one for the height window. A budget
// that can refuse the retry of the height the backlog is waiting for wedges
// sync exactly as completely as a count that can, and the height window tests
// cannot see it: their blocks are small enough that the budget is never near
// spent.
func (suite *SynchronizerTestSuite) TestBlockingHeightIsStillFetchableAtTheByteBound() {
	ctx := context.Background()
	h := suite.newBacklogHarness()
	const blocking = int64(1)
	const blockSize = maxPendingApplyBytes / 8

	var blockingPeer types.NodeID
	buffered := 0
	for attempt := 0; attempt < h.chainLen; attempt++ {
		resp := h.produce(ctx)
		if resp == nil {
			break
		}
		if resp.Block.Height == blocking {
			blockingPeer = resp.PeerID
			continue
		}
		h.deliverSized(ctx, resp, blockSize)
		buffered++
	}
	suite.Require().Equal(8, buffered, "the byte budget did not stop the backlog where it should")
	suite.Require().Less(buffered, maxOutstandingHeights,
		"the height window stopped this, so it says nothing about the byte budget")
	suite.Require().NotEmpty(blockingPeer)

	// The unanswered request runs out of time and is queued for a retry.
	h.timeout(ctx, blockingPeer, blocking)

	resp := h.produce(ctx)
	suite.Require().NotNil(resp,
		"the byte budget is spent and the height it is waiting for can never be requested again")
	suite.Require().EqualValues(blocking, resp.Block.Height)

	h.deliver(ctx, resp)

	suite.Require().Empty(h.pool.pendingToApply, "the backlog did not drain once the blocking height arrived")
	height, _ := h.pool.GetStatus()
	suite.Require().EqualValues(blocking+1+int64(buffered), height,
		"every buffered height must have been applied behind the one that arrived")
}

// TestBlockingHeightIsStillFetchableAtTheBound is the regression guard on the
// bound, and the more important of the pair: a bound that wedges sync is worse
// than no bound. With the window full, the one height that would unblock
// everything must still be issuable, and issuing it must drain the whole
// backlog.
func (suite *SynchronizerTestSuite) TestBlockingHeightIsStillFetchableAtTheBound() {
	ctx := context.Background()
	h := suite.newBacklogHarness()
	const blocking = int64(1)

	blockingPeer := h.fillWindow(ctx, blocking)
	suite.Require().Len(h.pool.pendingToApply, maxOutstandingHeights-1, "the window did not fill")
	suite.Require().NotEmpty(blockingPeer)

	// The unanswered request runs out of time and is queued for a retry.
	h.timeout(ctx, blockingPeer, blocking)

	resp := h.produce(ctx)
	suite.Require().NotNil(resp,
		"the window is full and the height it is waiting for can never be requested again")
	suite.Require().EqualValues(blocking, resp.Block.Height,
		"the retry of the blocking height must be what goes out first")

	h.deliver(ctx, resp)

	suite.Require().Empty(h.pool.pendingToApply, "the backlog did not drain once the blocking height arrived")
	height, _ := h.pool.GetStatus()
	suite.Require().EqualValues(blocking+maxOutstandingHeights, height,
		"every buffered height must have been applied behind the one that arrived")
}

// TestDroppingTheBlockingPeerLeavesTheHeightToTheTimeout pins what happens at
// the bound when the peer holding the request everything is waiting on is
// dropped for some other reason. Dropping a peer re-queues the heights it had
// already answered, and a request still in flight is not among them, so the
// height that would release the backlog is not rescued by the removal - the
// client's timeout is still the only thing that frees it.
func (suite *SynchronizerTestSuite) TestDroppingTheBlockingPeerLeavesTheHeightToTheTimeout() {
	ctx := context.Background()
	h := suite.newBacklogHarness()
	const blocking = int64(1)

	blockingPeer := h.fillWindow(ctx, blocking)
	suite.Require().Len(h.pool.pendingToApply, maxOutstandingHeights-1, "the window did not fill")
	suite.Require().NotEmpty(blockingPeer)

	h.pool.RemovePeer(blockingPeer)
	requeued := append([]int64(nil), h.pool.jobGen.pushedBack...)
	suite.Require().NotContains(requeued, blocking,
		"a request still in flight must not be re-queued by dropping its peer")

	// What the dropped peer had answered goes out again, and then the window
	// refuses new heights: the blocking height is still outstanding.
	for range requeued {
		resp := h.produce(ctx)
		suite.Require().NotNil(resp, "a re-queued height was starved by the window")
		suite.Require().NotEqualValues(blocking, resp.Block.Height)
	}
	suite.Require().Nil(h.produce(ctx), "the window must still refuse new heights")

	// The timeout is what frees it.
	h.timeout(ctx, blockingPeer, blocking)
	resp := h.produce(ctx)
	suite.Require().NotNil(resp, "the blocking height was stranded by dropping its peer")
	suite.Require().EqualValues(blocking, resp.Block.Height)
}

// TestInOrderSyncIsNotThrottledByTheBound checks that the window does not slow a
// healthy sync. With responses arriving in order there is never anything to
// buffer, so the pipeline must stay as full as the window allows and every
// applied block must let exactly one more height be requested.
func (suite *SynchronizerTestSuite) TestInOrderSyncIsNotThrottledByTheBound() {
	ctx := context.Background()
	h := suite.newBacklogHarness()

	// The producer runs ahead until the window is full, the state a healthy sync
	// runs in.
	fetched := make([]*BlockResponse, 0, h.chainLen)
	for attempt := 0; attempt < h.chainLen; attempt++ {
		resp := h.produce(ctx)
		if resp == nil {
			break
		}
		fetched = append(fetched, resp)
	}
	suite.Require().Equal(maxOutstandingHeights, len(fetched), "the window did not fill to its stated width")

	for applied := 0; applied < h.chainLen; applied++ {
		h.deliver(ctx, fetched[applied])
		height, _ := h.pool.GetStatus()
		suite.Require().EqualValues(applied+2, height, "in-order arrival did not advance the height")
		if len(fetched) == h.chainLen {
			continue
		}
		resp := h.produce(ctx)
		suite.Require().NotNil(resp, "the window did not slide when the block at height %d was applied", height-1)
		fetched = append(fetched, resp)
	}
	suite.Require().Empty(h.pool.pendingToApply, "an in-order sync must leave nothing buffered")
}

// TestRequeuedHeightsAreNotStarvedByTheBound checks that heights queued for a
// retry go out even while the window is refusing new ones. A re-queued height
// was inside the window when it was first requested and the window only moves
// up, so re-issuing it cannot widen the backlog - and holding it back is how a
// bound turns into a permanent stall, because the lowest re-queued height is
// typically the one everything else is waiting for.
func (suite *SynchronizerTestSuite) TestRequeuedHeightsAreNotStarvedByTheBound() {
	ctx := context.Background()
	h := suite.newBacklogHarness()
	const blocking = int64(1)

	h.fillWindow(ctx, blocking)
	suite.Require().Len(h.pool.pendingToApply, maxOutstandingHeights-1, "the window did not fill")

	// Dropping a peer re-queues every height it had answered.
	dropped := h.pool.pendingToApply[maxOutstandingHeights/2].PeerID
	suite.Require().NotEmpty(dropped)
	h.pool.RemovePeer(dropped)
	requeued := append([]int64(nil), h.pool.jobGen.pushedBack...)
	suite.Require().NotEmpty(requeued, "dropping a peer must re-queue the heights it had answered")

	reissued := make([]int64, 0, len(requeued))
	for range requeued {
		resp := h.produce(ctx)
		suite.Require().NotNil(resp, "a re-queued height was starved by the window")
		reissued = append(reissued, resp.Block.Height)
	}
	suite.Require().Equal(requeued, reissued, "re-queued heights must go out lowest first")

	suite.Require().Nil(h.produce(ctx),
		"with the retry queue drained the window must refuse new heights again")
}

// droppedRequestHandler answers no p2p message itself. Block responses are
// resolved by the client before a handler is consulted, and nothing else is
// exchanged in this test.
type droppedRequestHandler struct{}

func (droppedRequestHandler) Handle(_ context.Context, _ *client.Client, _ *p2p.Envelope) error {
	return nil
}

// TestClientTimeoutUnwedgesAFullWindow drives the whole no-wedge path with
// nothing stubbed between a request and its failure: a real block client on a
// fake clock, real workers, and the synchronizer's own producer and consumer
// goroutines.
//
// The bound's liveness rests on an unanswered request eventually settling as a
// failure, which is what re-queues the height and lets it past the bound. That
// happens in the block client, in another package, so a test that hands the
// consumer a failure directly assumes the very thing at issue. Here the peer
// drops the first request for the lowest height and answers its retry: the
// backlog fills to the window, the request that would release it is
// outstanding, and only the client's own timeout can turn it back into a job.
func (suite *SynchronizerTestSuite) TestClientTimeoutUnwedgesAFullWindow() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	chain := suite.backlogChain()
	const blocking = int64(1)

	// Peer selection must not depend on how fast the test runs, as in
	// newBacklogHarness.
	frozen := time.Now()
	flowrate.Now = func() time.Time { return frozen }
	defer func() { flowrate.Now = flowrate.TimeNow }()

	// The peer answers every request by posting a response back, except that it
	// silently drops the first request for the blocking height.
	//
	// It answers from its own goroutine, after a pause. A peer cannot answer
	// before the request has left the node, and the client only starts waiting
	// for a response once the send has returned, so a reply posted from inside
	// Send arrives before anything is waiting for it and is discarded as
	// unsolicited. The pause is the round trip that ordering comes from.
	responses := make(chan p2p.Envelope, 2*len(chain))
	var alreadyDropped atomic.Bool
	p2pChannel := p2pmocks.NewChannel(suite.T())
	p2pChannel.
		On("Send", mock.Anything, mock.Anything).
		Return(func(_ context.Context, envelope p2p.Envelope) error {
			req, ok := envelope.Message.(*blocksync.BlockRequest)
			if !ok {
				return nil
			}
			if req.Height == blocking && !alreadyDropped.Swap(true) {
				return nil
			}
			answer := p2p.Envelope{
				From:      envelope.To,
				ChannelID: p2p.BlockSyncChannel,
				Message:   chain[req.Height-1],
				Attributes: map[string]string{
					client.ResponseIDAttribute: envelope.Attributes[client.RequestIDAttribute],
				},
			}
			go func() {
				time.Sleep(5 * time.Millisecond)
				select {
				case responses <- answer:
				case <-ctx.Done():
				}
			}()
			return nil
		})
	p2pChannel.
		On("Receive", mock.Anything).
		Return(func(_ context.Context) p2p.ChannelIterator { return p2p.NewChannelIterator(responses) })
	p2pChannel.On("SendError", mock.Anything, mock.Anything).Maybe().Return(nil)
	p2pChannel.On("String").Maybe().Return("blocksync test channel")
	p2pChannel.On("Err").Maybe().Return(nil)

	clientClock := clockwork.NewFakeClock()
	blockClient := client.New(
		map[p2p.ChannelID]*p2p.ChannelDescriptor{
			p2p.BlockSyncChannel: {ID: p2p.BlockSyncChannel, Name: "blocksync"},
			p2p.ErrorChannel:     {ID: p2p.ErrorChannel, Name: "error"},
		},
		func(_ context.Context, _ *p2p.ChannelDescriptor) (p2p.Channel, error) { return p2pChannel, nil },
		client.WithClock(clientClock),
	)
	go func() {
		_ = blockClient.Consume(ctx, client.ConsumerParams{
			ReadChannels: []p2p.ChannelID{p2p.BlockSyncChannel},
			Handler:      droppedRequestHandler{},
		})
	}()

	suite.store.
		On("SaveBlock", mock.Anything, mock.Anything, mock.Anything).
		Maybe()
	suite.blockExec.
		On("ValidateBlock", mock.Anything, mock.Anything, mock.Anything).
		Maybe().
		Return(nil)
	expectApply(suite.blockExec, func(call *mock.Call) { call.Maybe() })

	applier := newBlockApplier(suite.blockExec, suite.store, applierWithState(suite.initialState))
	pool := NewSynchronizer(blocking, blockClient, applier,
		WithWorkerPool(workerpool.New(maxOutstandingHeights)))
	suite.Require().NoError(pool.Start(ctx))
	defer pool.Stop()
	for i := 0; i < maxOutstandingHeights/maxPendingRequestsPerPeer+4; i++ {
		pool.AddPeer(newPeerData(types.NodeID(fmt.Sprintf("dropping peer %d", i)), 1, int64(len(chain))))
	}

	// Everything above the dropped height fills the window and stops there.
	wantBytes := 0
	for height := blocking + 1; height < blocking+maxOutstandingHeights; height++ {
		wantBytes += chain[height-1].Block.Size()
	}
	suite.Require().Eventually(func() bool {
		_, pendingBytes := pool.backlog()
		return pendingBytes == wantBytes
	}, 30*time.Second, 5*time.Millisecond, "the backlog never filled to the window")
	height, _ := pool.GetStatus()
	suite.Require().EqualValues(blocking, height, "the dropped height must still be the one being waited for")

	// Nothing but the timeout can move it now.
	clientClock.Advance(time.Minute)

	suite.Require().Eventually(func() bool {
		height, _ := pool.GetStatus()
		return height == int64(len(chain))+1
	}, 60*time.Second, 10*time.Millisecond,
		"block sync did not finish after the dropped request timed out and was retried")
}

// TestConsumeJobResultKeepsPeerOnTransientFailure checks that a single failed
// block request does not drop the peer. Dropping it would also fail its other
// in-flight requests, each of which drops another peer in turn.
func (suite *SynchronizerTestSuite) TestConsumeJobResultKeepsPeerOnTransientFailure() {
	ctx := context.Background()

	peerID := types.NodeID("peer 1")
	resultCh := make(chan workerpool.Result, 1)
	wp := workerpool.New(1, workerpool.WithResultCh(resultCh))
	applier := newBlockApplier(suite.blockExec, suite.store, applierWithState(suite.initialState))
	pool := NewSynchronizer(1, suite.client, applier, WithWorkerPool(wp))
	pool.AddPeer(newPeerData(peerID, 1, 100))

	// stop one short of the threshold: the peer is kept, and the client mock has
	// no Send expectation, so reporting it would fail this test
	for i := int32(1); i < maxConsecutiveFailures; i++ {
		resultCh <- workerpool.Result{
			Err: &errBlockFetch{peerID: peerID, height: int64(i), err: errors.New("timeout")},
		}
		suite.Require().NoError(pool.consumeJobResult(ctx))
		suite.Require().Len(pool.peerStore.All(), 1, "peer dropped after %d failures", i)
	}

	// the next failure crosses the threshold and does report the peer
	suite.client.
		On("Send", mock.Anything, mock.Anything).
		Once().
		Return(nil)
	resultCh <- workerpool.Result{
		Err: &errBlockFetch{peerID: peerID, height: 99, err: errors.New("timeout")},
	}
	suite.Require().NoError(pool.consumeJobResult(ctx))
	suite.Require().Empty(pool.peerStore.All(), "peer must be dropped once it exceeds the threshold")
}

// expectApply sets up the pair of application calls the block applier makes for
// every block it applies - it runs the two halves of ApplyBlock separately so
// that the block store is advanced between them - and leaves the state
// unchanged. expect applies the same cardinality to both.
func expectApply(exec *mocks.Executor, expect func(*mock.Call)) {
	expect(exec.
		On("ProcessProposal", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(sm.CurrentRoundState{}, nil))
	expect(exec.
		On("FinalizeBlock", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(func(_ context.Context, state sm.State, _ sm.CurrentRoundState, _ types.BlockID, _ *types.Block, _ *types.Commit) sm.State {
			return state
		}, nil))
}
