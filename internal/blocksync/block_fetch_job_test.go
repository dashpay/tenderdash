package blocksync

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/tendermint/tendermint/internal/p2p/client"
	"github.com/tendermint/tendermint/internal/p2p/client/mocks"
	statefactory "github.com/tendermint/tendermint/internal/state/test/factory"
	"github.com/tendermint/tendermint/internal/test/factory"
	tmrequire "github.com/tendermint/tendermint/internal/test/require"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/libs/promise"
	"github.com/tendermint/tendermint/libs/workerpool"
	bcproto "github.com/tendermint/tendermint/proto/tendermint/blocksync"
	"github.com/tendermint/tendermint/types"
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
	peerStore.Put(suite.peer)
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
	peerStore.Put(suite.peer)
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

func (suite *BlockFetchJobTestSuite) promiseReject(err error) *promise.Promise[*bcproto.BlockResponse] {
	return promise.New(func(_ func(data *bcproto.BlockResponse), reject func(err error)) {
		reject(err)
	})
}

func (suite *BlockFetchJobTestSuite) promiseResolve(height int64) *promise.Promise[*bcproto.BlockResponse] {
	return promise.New(func(resolve func(data *bcproto.BlockResponse), _ func(err error)) {
		resolve(suite.responses[height-1])
	})
}

func (suite *BlockFetchJobTestSuite) getBlockReturnFunc(promiseFunc *promise.Promise[*bcproto.BlockResponse]) any {
	return func(_ context.Context, _ int64, _ types.NodeID) *promise.Promise[*bcproto.BlockResponse] {
		return promiseFunc
	}
}
