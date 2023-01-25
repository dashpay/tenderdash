package consensus

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/tendermint/tendermint/crypto"
	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	sm "github.com/tendermint/tendermint/internal/state"
	smmocks "github.com/tendermint/tendermint/internal/state/mocks"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/types"
	"github.com/tendermint/tendermint/types/mocks"
)

type BockExecutorTestSuite struct {
	suite.Suite

	blockExec     *blockExecutor
	mockPrivVal   *mocks.PrivValidator
	mockBlockExec *smmocks.Executor
}

func TestBockExecutor(t *testing.T) {
	suite.Run(t, new(BockExecutorTestSuite))
}

func (suite *BockExecutorTestSuite) SetupTest() {
	logger := log.NewTestingLogger(suite.T())
	suite.mockPrivVal = mocks.NewPrivValidator(suite.T())
	suite.mockBlockExec = smmocks.NewExecutor(suite.T())
	suite.blockExec = &blockExecutor{
		logger: logger,
		privValidator: privValidator{
			PrivValidator: suite.mockPrivVal,
			ProTxHash:     crypto.RandProTxHash(),
		},
		blockExec:          suite.mockBlockExec,
		proposedAppVersion: 0,
	}
}

func (suite *BockExecutorTestSuite) TestCreate() {
	ctx := context.Background()
	commitH99R0 := &types.Commit{
		Height: 99,
		Round:  0,
	}
	emptyCommit := types.NewCommit(0, 0, types.BlockID{}, nil)
	testCases := []struct {
		round         int32
		initialHeight int64
		height        int64
		lastCommit    *types.Commit
		wantCommit    *types.Commit
		wantBlock     *types.Block
		wantCRS       sm.CurrentRoundState
	}{
		{
			round:         0,
			height:        1,
			initialHeight: 1,
			wantCommit:    emptyCommit,
			wantBlock: &types.Block{
				Header: types.Header{
					Height:  1,
					AppHash: []byte("H1R0"),
				},
			},
			wantCRS: sm.CurrentRoundState{
				AppHash: []byte("H1R0"),
			},
		},
		{
			round:         0,
			height:        100,
			initialHeight: 1,
			lastCommit:    commitH99R0,
			wantCommit:    commitH99R0,
			wantBlock: &types.Block{
				Header: types.Header{
					Height:  1,
					AppHash: []byte("H100R0"),
				},
			},
			wantCRS: sm.CurrentRoundState{
				AppHash: []byte("H100R0"),
			},
		},
		{
			round:         0,
			height:        100,
			initialHeight: 1,
			lastCommit:    nil,
			wantCommit:    nil,
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("test-case #%d", i), func() {
			stateData := StateData{
				RoundState: cstypes.RoundState{
					Height:     tc.height,
					LastCommit: tc.lastCommit,
				},
				state: sm.State{
					InitialHeight: tc.initialHeight,
				},
			}
			if tc.wantCommit != nil {
				suite.mockBlockExec.
					On(
						"CreateProposalBlock",
						mock.AnythingOfType("*context.emptyCtx"),
						tc.height,
						tc.round,
						stateData.state,
						tc.wantCommit,
						suite.blockExec.privValidator.ProTxHash.Bytes(),
						suite.blockExec.proposedAppVersion,
					).
					Once().
					Return(tc.wantBlock, tc.wantCRS, nil)
			}
			actualBlock, err := suite.blockExec.create(ctx, &stateData, tc.round)
			require.NoError(suite.T(), err)
			require.Equal(suite.T(), tc.wantBlock, actualBlock)
			require.Equal(suite.T(), tc.wantCRS, stateData.CurrentRoundState)
		})
	}
}

func (suite *BockExecutorTestSuite) TestProcess() {
	ctx := context.Background()
	const round = int32(0)
	wantDefaultCRS := sm.CurrentRoundState{
		AppHash: []byte("want this app hash"),
	}
	processProposalCRS := sm.CurrentRoundState{
		Base: sm.State{
			LastBlockHeight: 99,
		},
		Params:      sm.RoundParams{Source: sm.ProcessProposalSource},
		Round:       0,
		AppHash:     []byte("1234"),
		ResultsHash: []byte("4321"),
	}
	testCases := []struct {
		header          types.Header
		crs             sm.CurrentRoundState
		wantCRS         sm.CurrentRoundState
		mustNotBeCalled bool
		wantErr         string
	}{
		{
			crs: sm.CurrentRoundState{
				Params: sm.RoundParams{Source: ""},
			},
			wantCRS: wantDefaultCRS,
		},
		{
			crs: sm.CurrentRoundState{
				Params: sm.RoundParams{Source: ""},
			},
			wantErr: "process-proposal error",
		},
		{
			crs: sm.CurrentRoundState{
				Params: sm.RoundParams{Source: sm.InitChainSource},
			},
			wantCRS: wantDefaultCRS,
		},
		{
			crs: sm.CurrentRoundState{
				Params: sm.RoundParams{Source: sm.PrepareProposalSource},
			},
			wantCRS: wantDefaultCRS,
		},
		{
			crs: processProposalCRS,
			// header doesn't match on processProposalCRS
			// AppHash is different
			header: types.Header{
				Height:      100,
				AppHash:     []byte(""),
				ResultsHash: []byte("4321"),
			},
			wantCRS: processProposalCRS,
		},
		{
			crs: processProposalCRS,
			header: types.Header{
				Height:      100,
				AppHash:     []byte("1234"),
				ResultsHash: []byte("4321"),
			},
			wantCRS:         processProposalCRS,
			mustNotBeCalled: true,
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("test-case #%d", i), func() {
			stateData := StateData{
				RoundState: cstypes.RoundState{
					ProposalBlock: &types.Block{
						Header: tc.header,
					},
					CurrentRoundState: tc.crs,
				},
				state: sm.State{},
			}
			if !tc.mustNotBeCalled {
				var wantErr error
				if tc.wantErr != "" {
					wantErr = errors.New(tc.wantErr)
				}
				suite.mockBlockExec.
					On(
						"ProcessProposal",
						mock.AnythingOfType("*context.emptyCtx"),
						stateData.ProposalBlock,
						round,
						stateData.state,
						true,
					).
					Once().
					Return(tc.wantCRS, wantErr)
			}
			err := suite.blockExec.ensureProcess(ctx, &stateData, round)
			assertError(suite.T(), tc.wantErr, err)
			require.Equal(suite.T(), tc.wantCRS, stateData.CurrentRoundState)
		})
	}
}

func assertError(t *testing.T, want string, err error) {
	if want == "" {
		require.NoError(t, err)
		return
	}
	require.ErrorContains(t, err, want)
}
