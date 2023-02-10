package blocksync

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/tendermint/tendermint/internal/blocksync/mocks"
	"github.com/tendermint/tendermint/internal/state/test/factory"
	tmrequire "github.com/tendermint/tendermint/internal/test/require"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/libs/promise"
	bcproto "github.com/tendermint/tendermint/proto/tendermint/blocksync"
	"github.com/tendermint/tendermint/types"
)

type BlockFetchJobTestSuite struct {
	suite.Suite

	responses []*bcproto.BlockResponse
	client    *mocks.BlockClient
	peer      *PeerData
	job       *blockFetchJob
}

func TestBlockFetchJob(t *testing.T) {
	suite.Run(t, new(BlockFetchJobTestSuite))
}

func (suite *BlockFetchJobTestSuite) SetupTest() {
	const chainLen = 10
	ctx := context.Background()

	valSet, privVals := types.MockValidatorSet()
	state := fakeInitialState(valSet)
	blocks := factory.MakeBlocks(ctx, suite.T(), chainLen+1, &state, privVals, 1)
	suite.responses = generateBlockResponses(suite.T(), blocks)
	suite.client = mocks.NewBlockClient(suite.T())
	suite.peer = newPeerData("peer-id", 1, 10)
	suite.job = &blockFetchJob{
		logger: log.NewNopLogger(),
		client: suite.client,
		peer:   suite.peer,
	}
}

func (suite *BlockFetchJobTestSuite) TestExecute() {
	ctx := context.Background()

	testCases := []struct {
		height       int64
		clientErr    error
		rejectErr    error
		wantErr      string
		wantTimedout bool
	}{
		{
			height: 2,
		},
		{
			height: 10,
		},
		{
			height:    9,
			clientErr: errors.New("client error"),
			wantErr:   "client error",
		},
		{
			height:       9,
			rejectErr:    errPeerNotResponded,
			wantErr:      errPeerNotResponded.Error(),
			wantTimedout: true,
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("test-case %d", i), func() {
			suite.client.
				On("GetBlock", mock.Anything, tc.height, suite.peer.peerID).
				Once().
				Return(func(_ context.Context, height int64, _ types.NodeID) *promise.Promise[*bcproto.BlockResponse] {
					return promise.New(func(resolve func(data *bcproto.BlockResponse), reject func(err error)) {
						if tc.rejectErr != nil {
							reject(tc.rejectErr)
							return
						}
						resolve(suite.responses[height-1])
					})
				}, tc.clientErr)
			suite.job.height = tc.height
			res := suite.job.Execute(ctx)
			suite.requireError(tc.wantErr, res.Err)
			suite.Require().Equal(tc.wantTimedout, suite.peer.didTimeout.Load())
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
	suite.Require().Equal(suite.peer, job.peer)

	cancel()
	job, err = jobGen.nextJob(ctx)
	suite.Require().Error(err)
}
