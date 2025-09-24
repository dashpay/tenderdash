package blocksync

import (
	"context"
	"errors"
	"fmt"
	mrand "math/rand"
	"sort"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/dashpay/tenderdash/internal/p2p"
	clientmocks "github.com/dashpay/tenderdash/internal/p2p/client/mocks"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/state/mocks"
	statefactory "github.com/dashpay/tenderdash/internal/state/test/factory"
	"github.com/dashpay/tenderdash/internal/test/factory"
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
		peers         []PeerData
		responses     []*BlockResponse
		peerID        types.NodeID
		wantPushBack  []int64
		wantPeers     []PeerData
		wantMaxHeight int64
	}{
		{
			peers:         peers,
			responses:     responses,
			peerID:        peerID1,
			wantPushBack:  []int64{1, 4, 5},
			wantPeers:     []PeerData{peers[1], peers[2]},
			wantMaxHeight: maxHeight,
		},
		{
			peers:         peers,
			responses:     responses,
			peerID:        peerID2,
			wantPushBack:  []int64{2, 7},
			wantPeers:     []PeerData{peers[0], peers[2]},
			wantMaxHeight: maxHeight,
		},
		{
			peers:         peers,
			responses:     responses,
			peerID:        peerID3,
			wantPushBack:  []int64{3, 6},
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
			pool.RemovePeer(tc.peerID)
			sort.Slice(pool.jobGen.pushedBack, func(i, j int) bool {
				return pool.jobGen.pushedBack[i] < pool.jobGen.pushedBack[j]
			})
			suite.Require().Equal(tc.wantPushBack, pool.jobGen.pushedBack)
			actualPeers := pool.peerStore.All()
			suite.Require().Equal(tc.wantPeers, actualPeers)
			suite.Require().Equal(tc.wantMaxHeight, pool.MaxPeerHeight())
		})
	}
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
