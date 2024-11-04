package consensus

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/state/mocks"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmrequire "github.com/dashpay/tenderdash/internal/test/require"
	"github.com/dashpay/tenderdash/libs/log"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

type PrevoterTestSuite struct {
	suite.Suite

	privVal         privValidator
	logger          *log.TestingLogger
	valSet          *types.ValidatorSet
	mockWAL         *mockWAL
	mockQueueSender *mockQueueSender
	mockExecutor    *mocks.Executor
	voteSigner      *voteSigner
	blockExecutor   *blockExecutor
	prevoter        *prevoter
	testSigner      testSigner
}

func TestPrevoter(t *testing.T) {
	suite.Run(t, new(PrevoterTestSuite))
}

func (suite *PrevoterTestSuite) SetupTest() {
	valSet, privVals := factory.MockValidatorSet()
	suite.valSet = valSet
	proTxHash, err := privVals[0].GetProTxHash(context.Background())
	suite.Require().NoError(err)
	suite.privVal = privValidator{
		PrivValidator: privVals[0],
		ProTxHash:     proTxHash,
	}
	suite.logger = log.NewTestingLogger(suite.T())
	suite.mockQueueSender = newMockQueueSender(suite.T())
	suite.mockWAL = newMockWAL(suite.T())
	suite.mockExecutor = mocks.NewExecutor(suite.T())
	suite.voteSigner = &voteSigner{
		privValidator: suite.privVal,
		logger:        suite.logger,
		queueSender:   suite.mockQueueSender,
		wal:           suite.mockWAL,
		voteExtender:  suite.mockExecutor,
	}
	suite.blockExecutor = &blockExecutor{
		logger:        suite.logger,
		privValidator: suite.privVal,
		blockExec:     suite.mockExecutor,
	}
	suite.prevoter = newPrevote(
		suite.logger,
		suite.voteSigner,
		suite.blockExecutor,
		NopMetrics(),
	)
	suite.testSigner = testSigner{
		privVals: privVals,
		valSet:   valSet,
		logger:   suite.logger,
	}
}

func (suite *PrevoterTestSuite) TestDo() {
	ctx := context.Background()
	suite.mockWAL.On("FlushAndSync").Maybe().Return(nil)
	currState := sm.CurrentRoundState{}

	validStateData := suite.makeValidStateData()
	testCases := []struct {
		state               sm.State
		rs                  cstypes.RoundState
		wantBlockID         types.BlockID
		ppErr               error
		wantErr             string
		wantLog             string
		mockProcessProposal bool
		mockValidateBlock   bool
	}{
		{
			// prevote is invalid, but returns nil
			state:       sm.State{Validators: suite.valSet},
			rs:          cstypes.RoundState{Validators: suite.valSet},
			wantBlockID: types.BlockID{},
			wantLog:     "proposal-block is nil",
		},
		{
			// process-proposal error
			state:               validStateData.state,
			rs:                  validStateData.RoundState,
			wantBlockID:         types.BlockID{},
			ppErr:               sm.ErrBlockRejected,
			wantErr:             sm.ErrBlockRejected.Error(),
			mockProcessProposal: true,
		},
		{
			state:               validStateData.state,
			rs:                  validStateData.RoundState,
			wantBlockID:         validStateData.Proposal.BlockID,
			mockProcessProposal: true,
			mockValidateBlock:   true,
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("test-case #%d", i), func() {
			stateData := StateData{
				state:      tc.state,
				RoundState: tc.rs,
			}
			if tc.mockProcessProposal {
				suite.mockExecutor.
					On("ProcessProposal", ctx, mock.Anything, int32(0), mock.Anything, true).
					Once().
					Return(currState, tc.ppErr)
			}
			if tc.mockValidateBlock {
				suite.mockExecutor.
					On("ValidateBlockWithRoundState", ctx, mock.Anything, mock.Anything, mock.Anything).
					Once().
					Return(nil)
			}
			fn := mock.MatchedBy(func(voteMsg *VoteMessage) bool {
				return tc.wantBlockID.Equals(voteMsg.Vote.BlockID)
			})
			if tc.wantLog != "" {
				suite.logger.AssertMatch(regexp.MustCompile(tc.wantLog))
			}
			suite.mockQueueSender.
				On("send", mock.Anything, fn, types.NodeID("")).
				Once().
				Return(nil)
			err := suite.prevoter.Do(ctx, &stateData)
			tmrequire.Error(suite.T(), tc.wantErr, err)
		})
	}
}

func (suite *PrevoterTestSuite) TestCheckProposalBlock() {
	testCases := []struct {
		modifier func(stateData *StateData)
		want     bool
	}{
		{
			modifier: func(stateData *StateData) {
				stateData.Proposal.POLRound = 0
			},
			want: false,
		},
		{
			modifier: func(stateData *StateData) {
				stateData.LockedRound = 0
				stateData.LockedBlock = &types.Block{}
			},
			want: false,
		},
		{
			modifier: func(stateData *StateData) {
				stateData.LockedRound = 0
			},
			want: true,
		},
		{
			modifier: func(stateData *StateData) {
				stateData.LockedBlock = &types.Block{}
			},
			want: true,
		},
		{
			want: true,
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("test-case #%d", i), func() {
			stateData := suite.makeValidStateData()
			if tc.modifier != nil {
				tc.modifier(&stateData)
			}
			suite.Equal(tc.want, suite.prevoter.checkProposalBlock(stateData.RoundState))
		})
	}
}

func (suite *PrevoterTestSuite) makeHeightVoteSetMaj23(stateData StateData, round int32) *cstypes.HeightVoteSet {
	ctx := context.Background()
	vote1 := types.Vote{
		Type:               tmproto.PrevoteType,
		Height:             stateData.Height,
		Round:              round,
		BlockID:            stateData.Proposal.BlockID,
		ValidatorProTxHash: suite.valSet.Validators[0].ProTxHash,
		ValidatorIndex:     0,
	}
	vote2 := types.Vote{
		Type:               tmproto.PrevoteType,
		Height:             stateData.Height,
		Round:              round,
		BlockID:            stateData.Proposal.BlockID,
		ValidatorProTxHash: suite.valSet.Validators[1].ProTxHash,
		ValidatorIndex:     1,
	}
	err := suite.testSigner.signVotes(ctx, &vote1, &vote2)
	suite.NoError(err)
	votesMaj23 := cstypes.NewHeightVoteSet("test-chain", 1000, suite.valSet)
	_, _ = votesMaj23.AddVote(&vote1)
	_, _ = votesMaj23.AddVote(&vote2)
	return votesMaj23
}

func (suite *PrevoterTestSuite) TestCheckPrevoteMaj23() {
	validStateData := suite.makeValidStateData()
	validStateData.Votes = suite.makeHeightVoteSetMaj23(validStateData, 0)
	testCases := []struct {
		modifier func(stateData *StateData)
		want     bool
	}{
		{
			modifier: func(stateData *StateData) {
				stateData.Proposal.POLRound = 0
				stateData.Round = 1
			},
			want: true,
		},
		{
			modifier: func(stateData *StateData) {
				stateData.Proposal.POLRound = 0
				stateData.LockedRound = 1
				stateData.Round = 1
			},
			want: true,
		},
		{
			modifier: func(stateData *StateData) {
				stateData.LockedBlock = &types.Block{}
				stateData.Proposal.POLRound = 0
				stateData.LockedRound = 1
				stateData.Round = 1
			},
			want: false,
		},
		{
			modifier: func(stateData *StateData) {
				stateData.Proposal.POLRound = 0
				stateData.Round = 0
			},
			want: false,
		},
		{
			modifier: func(stateData *StateData) {
				stateData.Proposal.POLRound = -1
				stateData.Votes = suite.makeHeightVoteSetMaj23(validStateData, 0)
			},
			want: false,
		},
		{
			modifier: func(stateData *StateData) {
				stateData.ProposalBlock = &types.Block{}
			},
			want: false,
		},
		{
			modifier: func(stateData *StateData) {
				stateData.Votes = cstypes.NewHeightVoteSet("test-chain", 1000, suite.valSet)
			},
			want: false,
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("test-case #%d", i), func() {
			stateData := validStateData
			if tc.modifier != nil {
				tc.modifier(&stateData)
			}
			suite.Equal(tc.want, suite.prevoter.checkPrevoteMaj23(stateData.RoundState))
		})
	}
}

func (suite *PrevoterTestSuite) makeValidStateData() StateData {
	now := tmtime.Now()
	validState := sm.State{
		InitialHeight: 1000,
		LastBlockTime: now,
		Validators:    suite.valSet,
	}
	block := &types.Block{
		Header: types.Header{
			Time:           now,
			ValidatorsHash: []byte{1, 2, 3, 4},
		},
		Data: types.Data{
			Txs: types.Txs{[]byte{1}, []byte{2}},
		},
		LastCommit: &types.Commit{},
	}
	validBlockID := types.BlockID{Hash: block.Hash()}
	validRoundState := cstypes.RoundState{
		Height:        1000,
		ProposalBlock: block,
		LockedBlock:   block,
		LockedRound:   -1,
		Proposal: &types.Proposal{
			Height:    1000,
			Timestamp: now,
			POLRound:  -1,
			BlockID:   validBlockID,
		},
		Validators: suite.valSet,
		Votes:      cstypes.NewHeightVoteSet("test-chain", 1000, suite.valSet),
	}
	return StateData{
		state:      validState,
		RoundState: validRoundState,
	}
}
