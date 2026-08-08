package blocksync

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/dashpay/tenderdash/internal/p2p/client"
	"github.com/dashpay/tenderdash/internal/p2p/client/mocks"
	statefactory "github.com/dashpay/tenderdash/internal/state/test/factory"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmrequire "github.com/dashpay/tenderdash/internal/test/require"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/promise"
	"github.com/dashpay/tenderdash/libs/workerpool"
	bcproto "github.com/dashpay/tenderdash/proto/tendermint/blocksync"
	"github.com/dashpay/tenderdash/types"
)

type BlockFetchJobTestSuite struct {
	suite.Suite

	responses []*bcproto.BlockResponse
	client    *mocks.BlockClient
	peer      PeerData
}

func TestBlockFetchJob(t *testing.T) {
	suite.Run(t, new(BlockFetchJobTestSuite))
}

func (suite *BlockFetchJobTestSuite) SetupTest() {
	const chainLen = 10
	ctx := context.Background()

	valSet, privVals := factory.MockValidatorSet()
	state := fakeInitialState(valSet)
	blocks := statefactory.MakeBlocks(ctx, suite.T(), chainLen+1, &state, privVals, 1)
	suite.responses = generateBlockResponses(suite.T(), blocks)
	suite.client = mocks.NewBlockClient(suite.T())
	suite.peer = newPeerData("peer-id", 1, 10)
}

func (suite *BlockFetchJobTestSuite) TestExecute() {
	ctx := context.Background()

	testCases := []struct {
		height        int64
		clientErr     error
		wantErr       string
		wantTimedout  bool
		promiseReturn *promise.Promise[*bcproto.BlockResponse]
	}{
		{
			height:        2,
			promiseReturn: suite.promiseResolve(2),
		},
		{
			height:        10,
			promiseReturn: suite.promiseResolve(10),
		},
		{
			height:        9,
			clientErr:     errors.New("client error"),
			wantErr:       "client error",
			promiseReturn: suite.promiseResolve(9),
		},
		{
			height:        9,
			wantErr:       client.ErrPeerNotResponded.Error(),
			promiseReturn: suite.promiseReject(client.ErrPeerNotResponded),
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("%d", i), func() {
			suite.client.
				On("GetBlock", mock.Anything, tc.height, suite.peer.peerID).
				Once().
				Return(suite.getBlockReturnFunc(tc.promiseReturn), tc.clientErr)
			handler := blockFetchJobHandler(suite.client, suite.peer, tc.height)
			res := handler(ctx)
			suite.requireError(tc.wantErr, res.Err)
		})
	}
}

func (suite *BlockFetchJobTestSuite) TestExecuteHeightMismatch() {
	ctx := context.Background()
	const requested = int64(5)
	// The peer answers a request for height 5 with a valid block+commit at height 6.
	mismatched := suite.responses[6-1]
	suite.client.
		On("GetBlock", mock.Anything, requested, suite.peer.peerID).
		Once().
		Return(suite.getBlockReturnFunc(suite.promiseResolveResponse(mismatched)), nil)
	handler := blockFetchJobHandler(suite.client, suite.peer, requested)
	res := handler(ctx)
	suite.requireError("peer sent block at height 6, requested height 5", res.Err)
}

func TestBlockResponseValidate(t *testing.T) {
	block := func(h int64) *types.Block {
		return &types.Block{Header: types.Header{Height: h}}
	}
	commit := func(h int64) *types.Commit {
		return &types.Commit{Height: h}
	}
	testCases := []struct {
		name    string
		resp    BlockResponse
		wantErr string
	}{
		{
			name:    "nil block, nil commit",
			resp:    BlockResponse{},
			wantErr: "block response without a block",
		},
		{
			name:    "nil block, non-nil commit",
			resp:    BlockResponse{Commit: commit(10)},
			wantErr: "block response without a block",
		},
		{
			name:    "block, nil commit",
			resp:    BlockResponse{Block: block(10)},
			wantErr: "a block without a commit at height 10",
		},
		{
			name:    "height mismatch",
			resp:    BlockResponse{Block: block(10), Commit: commit(11)},
			wantErr: "heights don't match",
		},
		{
			name: "valid",
			resp: BlockResponse{Block: block(10), Commit: commit(10)},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmrequire.Error(t, tc.wantErr, tc.resp.Validate())
		})
	}
}

func (suite *BlockFetchJobTestSuite) requireError(wantErr string, err error) {
	tmrequire.Error(suite.T(), wantErr, err)
	bfErr := &errBlockFetch{}
	if err != nil {
		suite.ErrorAs(err, &bfErr)
	}
}

func (suite *BlockFetchJobTestSuite) TestJobGeneratorNextJob() {
	ctx, cancel := context.WithCancel(context.Background())

	logger := log.NewNopLogger()
	peerStore := NewInMemPeerStore()
	peerStore.Put(suite.peer.peerID, suite.peer)
	jobGen := newJobGenerator(5, logger, suite.client, peerStore)

	job, err := jobGen.nextJob(ctx)
	suite.Require().NoError(err)
	suite.Require().NotNil(job)

	cancel()
	_, err = jobGen.nextJob(ctx)
	suite.Require().Error(err)
}

func (suite *BlockFetchJobTestSuite) TestGeneratorNextJobWaitForPeerAndPushBackHeight() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := log.NewNopLogger()
	peerStore := NewInMemPeerStore()
	jobGen := newJobGenerator(5, logger, suite.client, peerStore)
	jobCh := make(chan *workerpool.Job, 2)
	nextJobCh := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-nextJobCh:
				job, err := jobGen.nextJob(ctx)
				suite.Require().NoError(err)
				jobCh <- job
			}
		}
	}()
	nextJobCh <- struct{}{}
	jobGen.pushBack(9)
	peerStore.Put(suite.peer.peerID, suite.peer)
	nextJobCh <- struct{}{}
	heightCheck := mock.MatchedBy(func(height int64) bool {
		return suite.Contains([]int64{5, 9}, height)
	})
	suite.client.
		On("GetBlock", mock.Anything, heightCheck, mock.Anything).
		Twice().
		Return(func(_ context.Context, height int64, _ types.NodeID) *promise.Promise[*bcproto.BlockResponse] {
			return suite.promiseResolve(height)
		}, nil)
	suite.Eventually(func() bool {
		job1 := <-jobCh
		res1 := job1.Execute(ctx)
		resp1 := res1.Value.(*BlockResponse)
		job2 := <-jobCh
		res2 := job2.Execute(ctx)
		resp2 := res2.Value.(*BlockResponse)
		return suite.Equal(suite.peer.peerID, resp1.PeerID) &&
			suite.Equal(suite.peer.peerID, resp2.PeerID) &&
			suite.Contains([]int64{5, 9}, resp1.Block.Height) &&
			suite.Contains([]int64{5, 9}, resp2.Block.Height)
	}, 10*time.Millisecond, 5*time.Millisecond)
}

// TestJobGeneratorPushBack pins the scope of the pushBack dedupe guard: a height
// still waiting in the queue is not queued twice, while a height already handed to
// a worker is queued again, because pushedBack stops tracking it once dispatched.
func (suite *BlockFetchJobTestSuite) TestJobGeneratorPushBack() {
	const height = int64(9)
	jobGen := newJobGenerator(5, log.NewNopLogger(), suite.client, NewInMemPeerStore())

	jobGen.pushBack(height)
	jobGen.pushBack(height)
	suite.Require().Equal([]int64{height}, jobGen.pushedBack)

	// Dispatching the height empties the queue, so the guard no longer sees it.
	suite.Require().Equal(height, jobGen.nextHeight())
	suite.Require().Empty(jobGen.pushedBack)
	jobGen.pushBack(height)
	suite.Require().Equal([]int64{height}, jobGen.pushedBack)
}

// TestShouldJobBeGenerated asks the admission decision directly, one arm at a
// time. The synchronizer tests drive it through the whole pipeline, which is
// what makes them evidence about behavior, but it also means an arm that
// wrongly admits shows up as job generation walking into heights nobody holds
// and waiting there for a peer - a stall, not an answer. Here the question is
// asked and the answer read.
func TestShouldJobBeGenerated(t *testing.T) {
	const applyHeight = int64(100)
	const servedTo = applyHeight + 1000

	testCases := []struct {
		name         string
		nextHeight   int64
		pendingBytes int
		pushedBack   []int64
		peerHeight   int64
		want         bool
	}{
		{
			name:       "a peer holds the next height and nothing is held back",
			nextHeight: applyHeight,
			peerHeight: servedTo,
			want:       true,
		},
		{
			name:       "the next height is above every peer",
			nextHeight: applyHeight + 1,
			peerHeight: applyHeight,
			want:       false,
		},
		{
			name:       "one height short of the window",
			nextHeight: applyHeight + maxOutstandingHeights - 1,
			peerHeight: servedTo,
			want:       true,
		},
		{
			name:       "the window is full",
			nextHeight: applyHeight + maxOutstandingHeights,
			peerHeight: servedTo,
			want:       false,
		},
		{
			name:         "one byte short of the budget",
			nextHeight:   applyHeight,
			pendingBytes: maxPendingApplyBytes - 1,
			peerHeight:   servedTo,
			want:         true,
		},
		{
			name:         "the budget is spent",
			nextHeight:   applyHeight,
			pendingBytes: maxPendingApplyBytes,
			peerHeight:   servedTo,
			want:         false,
		},
		{
			// the anti-wedge property: whatever else is full, the height the
			// backlog is waiting for can still be asked for again
			name:         "a retry goes out past both limits",
			nextHeight:   applyHeight + maxOutstandingHeights,
			pendingBytes: maxPendingApplyBytes,
			pushedBack:   []int64{applyHeight},
			peerHeight:   servedTo,
			want:         true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			peerStore := NewInMemPeerStore(newPeerData("peer", 1, tc.peerHeight))
			jobGen := newJobGenerator(tc.nextHeight, log.NewNopLogger(), nil, peerStore)
			jobGen.pushBack(tc.pushedBack...)
			require.Equal(t, tc.want, jobGen.shouldJobBeGenerated(applyHeight, tc.pendingBytes))
		})
	}
}

func (suite *BlockFetchJobTestSuite) promiseReject(err error) *promise.Promise[*bcproto.BlockResponse] {
	return promise.New(func(_ func(data *bcproto.BlockResponse), reject func(err error)) {
		reject(err)
	})
}

func (suite *BlockFetchJobTestSuite) promiseResolve(height int64) *promise.Promise[*bcproto.BlockResponse] {
	return suite.promiseResolveResponse(suite.responses[height-1])
}

func (suite *BlockFetchJobTestSuite) promiseResolveResponse(resp *bcproto.BlockResponse) *promise.Promise[*bcproto.BlockResponse] {
	return promise.New(func(resolve func(data *bcproto.BlockResponse), _ func(err error)) {
		resolve(resp)
	})
}

func (suite *BlockFetchJobTestSuite) getBlockReturnFunc(promiseFunc *promise.Promise[*bcproto.BlockResponse]) any {
	return func(_ context.Context, _ int64, _ types.NodeID) *promise.Promise[*bcproto.BlockResponse] {
		return promiseFunc
	}
}

// TestJobGeneratorPushBackOrdersLowestFirst checks that re-queued heights are
// retried lowest first, regardless of the order they were handed over in.
//
// Only the lowest missing height lets applyBlock make progress, and dropPeer
// collects heights by ranging a map, so arrival order is arbitrary. Retrying in
// that order can spend the no-progress window fetching heights that cannot be
// applied until an earlier one lands.
func (suite *BlockFetchJobTestSuite) TestJobGeneratorPushBackOrdersLowestFirst() {
	jobGen := newJobGenerator(5, log.NewNopLogger(), suite.client, NewInMemPeerStore())

	jobGen.pushBack(30, 10, 20)
	suite.Require().Equal([]int64{10, 20, 30}, jobGen.pushedBack)

	// a height arriving later still sorts ahead of what is already queued
	jobGen.pushBack(5)
	suite.Require().Equal([]int64{5, 10, 20, 30}, jobGen.pushedBack)

	// and it is the one dispatched next
	suite.Require().EqualValues(5, jobGen.nextHeight())
	suite.Require().EqualValues(10, jobGen.nextHeight())

	// duplicates are still skipped across a batch
	jobGen.pushBack(20, 40, 20)
	suite.Require().Equal([]int64{20, 30, 40}, jobGen.pushedBack)
}
