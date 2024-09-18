package consensus

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	selectproposer "github.com/dashpay/tenderdash/internal/consensus/versioned/selectproposer"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/state/mocks"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmrequire "github.com/dashpay/tenderdash/internal/test/require"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

type ProposalerTestSuite struct {
	suite.Suite

	proposer          *Proposaler
	proposerSelector  selectproposer.ProposerSelector
	mockBlockExec     *mocks.Executor
	mockPrivVals      []types.PrivValidator
	mockValSet        *types.ValidatorSet
	msgInfoQueue      *msgInfoQueue
	committedState    sm.State
	commitH99R0       types.Commit
	proposerProTxHash types.ProTxHash
	blockH100R0       *types.Block
}

func TestProposaler(t *testing.T) {
	suite.Run(t, new(ProposalerTestSuite))
}

func (suite *ProposalerTestSuite) SetupTest() {
	logger := log.NewTestingLogger(suite.T())
	metrics := NopMetrics()
	valSet, privVals := factory.MockValidatorSet()
	suite.mockPrivVals = privVals
	suite.mockValSet = valSet
	privVal := privValidator{
		PrivValidator: privVals[0],
		ProTxHash:     valSet.Validators[0].ProTxHash,
	}
	suite.mockBlockExec = mocks.NewExecutor(suite.T())
	blockExec := &blockExecutor{
		logger:             logger,
		privValidator:      privVal,
		blockExec:          suite.mockBlockExec,
		proposedAppVersion: 0,
	}
	lastBlockID := types.BlockID{
		Hash: tmbytes.MustHexDecode("524F1D03D1D81E94A099042736D40BD9681B867321443FF58A4568E274DBD83B"),
	}
	suite.committedState = sm.State{
		ChainID:                          "test-chain",
		InitialHeight:                    1,
		LastBlockHeight:                  99,
		LastBlockID:                      lastBlockID,
		LastBlockTime:                    time.Time{},
		LastCoreChainLockedBlockHeight:   0,
		Validators:                       suite.mockValSet,
		LastValidators:                   suite.mockValSet,
		LastHeightValidatorsChanged:      1,
		ConsensusParams:                  types.ConsensusParams{},
		LastHeightConsensusParamsChanged: 1,
	}
	suite.commitH99R0 = types.Commit{
		Height:                  99,
		Round:                   0,
		BlockID:                 suite.committedState.LastBlockID,
		QuorumHash:              suite.mockValSet.QuorumHash,
		ThresholdBlockSignature: make([]byte, 96),
	}
	suite.msgInfoQueue = newMsgInfoQueue()
	go suite.msgInfoQueue.fanIn(context.Background())
	suite.proposer = &Proposaler{
		logger:         logger,
		metrics:        metrics,
		privVal:        privVal,
		msgInfoQueue:   suite.msgInfoQueue,
		blockExec:      blockExec,
		committedState: suite.committedState,
	}
	var err error
	suite.proposerSelector, err = selectproposer.NewProposerSelector(
		suite.committedState.ConsensusParams,
		valSet,
		0,
		0,
		nil,
	)
	if err != nil {
		panic(fmt.Errorf("failed to create validator scoring strategy: %w", err))
	}

	suite.proposerProTxHash = suite.proposerSelector.MustGetProposer(100, 0).ProTxHash
	suite.blockH100R0 = suite.committedState.MakeBlock(100, []types.Tx{}, &suite.commitH99R0, nil, suite.proposerProTxHash, 0)
}

func (suite *ProposalerTestSuite) TearDownTest() {
	suite.msgInfoQueue.stop()
}

func (suite *ProposalerTestSuite) TestSet() {
	ctx := context.Background()
	blockID := suite.blockH100R0.BlockID(nil)
	state := suite.committedState
	proposalH100R0 := types.NewProposal(100, state.LastCoreChainLockedBlockHeight, 0, -1, blockID, suite.blockH100R0.Header.Time)
	suite.signProposal(ctx, proposalH100R0)
	emptyProposal := types.Proposal{}
	receivedAt := time.Date(2023, 1, 31, 11, 0, 0, 0, time.UTC)
	testCases := []struct {
		proposal        types.Proposal
		rs              cstypes.RoundState
		receivedAt      time.Time
		wantProposal    *types.Proposal
		wantReceiveTime time.Time
		wantErr         string
	}{
		{
			// doesn't accept the proposal due to RoundState.Proposal is not nil
			rs:              cstypes.RoundState{Proposal: &emptyProposal},
			wantProposal:    &emptyProposal,
			receivedAt:      receivedAt,
			wantReceiveTime: time.Time{},
		},
		{
			// doesn't accept the proposal due to RoundState.Height != Proposal.Height
			rs:         cstypes.RoundState{Height: 101},
			proposal:   types.Proposal{Height: 100},
			receivedAt: receivedAt,
		},
		{
			// doesn't accept the proposal due to RoundState.Round != Proposal.Round
			rs:         cstypes.RoundState{Round: 0},
			proposal:   types.Proposal{Round: 1},
			receivedAt: receivedAt,
		},
		{
			// invalid POLRound
			rs:         cstypes.RoundState{Height: 100, Round: 0},
			proposal:   types.Proposal{Height: 100, Round: 0, POLRound: -2},
			receivedAt: receivedAt,
			wantErr:    ErrInvalidProposalPOLRound.Error(),
		},
		{
			// invalid POLRound
			rs:         cstypes.RoundState{Height: 100, Round: 0},
			proposal:   types.Proposal{Height: 100, Round: 0, POLRound: 0},
			receivedAt: receivedAt,
			wantErr:    ErrInvalidProposalPOLRound.Error(),
		},
		{
			rs: cstypes.RoundState{Height: 100,
				Round:            0,
				Validators:       suite.mockValSet,
				ProposerSelector: suite.proposerSelector,
			},
			proposal:        *proposalH100R0,
			receivedAt:      receivedAt,
			wantProposal:    proposalH100R0,
			wantReceiveTime: receivedAt,
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("%d", i), func() {
			err := suite.proposer.Set(&tc.proposal, tc.receivedAt, &tc.rs)
			tmrequire.Error(suite.T(), tc.wantErr, err)
			suite.Require().Equal(tc.wantProposal, tc.rs.Proposal)
			suite.Require().Equal(tc.wantReceiveTime, tc.rs.ProposalReceiveTime)
		})
	}
}

func (suite *ProposalerTestSuite) TestDecide() {
	ctx := context.Background()
	blockID := suite.blockH100R0.BlockID(nil)
	blockParts, err := suite.blockH100R0.MakePartSet(types.BlockPartSizeBytes)
	suite.Require().NoError(err)
	state := suite.committedState
	proposalH100R0 := types.NewProposal(100, state.LastCoreChainLockedBlockHeight, 0, 0, blockID, suite.blockH100R0.Header.Time)
	suite.signProposal(ctx, proposalH100R0)
	vs, err := selectproposer.NewProposerSelector(types.ConsensusParams{}, suite.mockValSet, 100, 0, nil)
	suite.Require().NoError(err)
	testCases := []struct {
		height       int64
		round        int32
		rs           cstypes.RoundState
		mockFn       func(rs cstypes.RoundState)
		wantProposal *types.Proposal
		wantErr      string
	}{
		{
			height: 100,
			round:  0,
			rs: cstypes.RoundState{
				Height:           100,
				Round:            0,
				Validators:       suite.mockValSet,
				ValidBlock:       nil,
				LastCommit:       &suite.commitH99R0,
				ValidRound:       0,
				ProposerSelector: vs,
			},
			mockFn: func(rs cstypes.RoundState) {
				suite.mockBlockExec.
					On("CreateProposalBlock",
						mock.Anything,
						rs.Height,
						rs.Round,
						mock.Anything,
						rs.LastCommit,
						suite.proposerProTxHash.Bytes(),
						uint64(0)).
					Once().
					Return(suite.blockH100R0, sm.CurrentRoundState{}, nil)
			},
			wantProposal: proposalH100R0,
		},
		{
			height: 100,
			round:  0,
			rs: cstypes.RoundState{
				Height:             100,
				Round:              0,
				Validators:         suite.mockValSet,
				ValidBlock:         suite.blockH100R0,
				ValidBlockParts:    blockParts,
				ValidBlockRecvTime: suite.blockH100R0.Time.Add(100 * time.Millisecond),
				LastCommit:         &suite.commitH99R0,
				ValidRound:         0,
				ProposerSelector:   vs,
			},
			wantProposal: proposalH100R0,
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("test-case #%d", i), func() {
			if tc.mockFn != nil {
				tc.mockFn(tc.rs)
			}
			err := suite.proposer.Create(ctx, tc.height, tc.round, &tc.rs)
			tmrequire.Error(suite.T(), tc.wantErr, err)
			if tc.wantProposal == nil {
				suite.Require().Len(suite.msgInfoQueue.sender.peerQueue.ch, 0)
				suite.Require().Len(suite.msgInfoQueue.sender.internalQueue.ch, 0)
				suite.Require().Len(suite.msgInfoQueue.reader.outCh, 0)
				return
			}
			msg := <-suite.msgInfoQueue.read()
			proposalMessage := msg.Msg.(*ProposalMessage)
			suite.Require().Equal(tc.wantProposal, proposalMessage.Proposal)
			msg = <-suite.msgInfoQueue.read()
			blockPartMessage := msg.Msg.(*BlockPartMessage)
			suite.Require().Equal(tc.rs.Height, blockPartMessage.Height)
			suite.Require().Equal(tc.rs.Round, blockPartMessage.Round)
		})
	}
}

func (suite *ProposalerTestSuite) TestVerifyProposal() {
	ctx := context.Background()
	blockID := suite.blockH100R0.BlockID(nil)
	state := suite.committedState
	proposalH100R0 := types.NewProposal(100, state.LastCoreChainLockedBlockHeight, 0, 0, blockID, suite.blockH100R0.Header.Time)
	suite.signProposal(ctx, proposalH100R0)
	proposalH100R0wrongSig := *proposalH100R0
	proposalH100R0wrongSig.Signature = make([]byte, 96)
	valSet := *suite.mockValSet
	proposer := valSet.Proposer()
	proposer.PubKey = nil
	idx, _ := valSet.GetByProTxHash(proposer.ProTxHash)
	valSet.Validators[int(idx)] = proposer

	testCases := []struct {
		proposal *types.Proposal
		rs       cstypes.RoundState
		wantErr  string
	}{
		{
			proposal: proposalH100R0,
			rs: cstypes.RoundState{
				Validators:       suite.mockValSet,
				ProposerSelector: suite.proposerSelector,
			},
		},
		{
			proposal: &proposalH100R0wrongSig,
			rs: cstypes.RoundState{
				Validators:       suite.mockValSet,
				ProposerSelector: suite.proposerSelector,
			},
			wantErr: ErrInvalidProposalSignature.Error(),
		},
		{
			proposal: proposalH100R0,
			rs: cstypes.RoundState{
				Commit:           nil,
				Validators:       &valSet,
				ProposerSelector: suite.proposerSelector,
			},
			wantErr: ErrUnableToVerifyProposal.Error(),
		},
		{
			proposal: proposalH100R0,
			rs: cstypes.RoundState{
				Commit:           &types.Commit{Height: 99},
				Validators:       &valSet,
				ProposerSelector: suite.proposerSelector,
			},
			wantErr: ErrUnableToVerifyProposal.Error(),
		},
		{
			proposal: proposalH100R0,
			rs: cstypes.RoundState{
				Commit:           &types.Commit{Height: 100, Round: 1},
				Validators:       &valSet,
				ProposerSelector: suite.proposerSelector,
			},
			wantErr: ErrUnableToVerifyProposal.Error(),
		},
		{
			proposal: proposalH100R0,
			rs: cstypes.RoundState{
				Commit:           &types.Commit{Height: 100, Round: 0, BlockID: types.BlockID{Hash: nil}},
				Validators:       &valSet,
				ProposerSelector: suite.proposerSelector,
			},
			wantErr: ErrInvalidProposalForCommit.Error(),
		},
		{
			proposal: proposalH100R0,
			rs: cstypes.RoundState{
				Commit:           &types.Commit{Height: 100, Round: 0, BlockID: proposalH100R0.BlockID},
				Validators:       &valSet,
				ProposerSelector: suite.proposerSelector,
			},
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("test-case #%d", i), func() {
			err := suite.proposer.verifyProposal(tc.proposal, &tc.rs)
			tmrequire.Error(suite.T(), tc.wantErr, err)
		})
	}
}

func (suite *ProposalerTestSuite) signProposal(ctx context.Context, proposal *types.Proposal) {
	protoProposal := proposal.ToProto()
	_, err := suite.mockPrivVals[0].SignProposal(ctx, suite.committedState.ChainID, suite.mockValSet.QuorumType, suite.mockValSet.QuorumHash, protoProposal)
	proposal.Signature = protoProposal.Signature
	suite.Require().NoError(err)
}
