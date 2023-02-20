package consensus

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/tendermint/tendermint/crypto"
	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	"github.com/tendermint/tendermint/internal/p2p"
	mocks2 "github.com/tendermint/tendermint/internal/p2p/mocks"
	"github.com/tendermint/tendermint/internal/state/mocks"
	"github.com/tendermint/tendermint/internal/test/factory"
	"github.com/tendermint/tendermint/libs/log"
	tmrand "github.com/tendermint/tendermint/libs/rand"
	tmcons "github.com/tendermint/tendermint/proto/tendermint/consensus"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
)

type GossiperSuiteTest struct {
	suite.Suite

	ps         *PeerState
	gossiper   *msgGossiper
	sender     *p2pMsgSender
	blockStore *mocks.BlockStore
	stateCh    *mocks2.Channel
	dataCh     *mocks2.Channel
	voteCh     *mocks2.Channel

	chainID   string
	proTxHash types.ProTxHash
	valSet    *types.ValidatorSet
	privVals  []types.PrivValidator
}

func TestGossiper(t *testing.T) {
	suite.Run(t, new(GossiperSuiteTest))
}

func (suite *GossiperSuiteTest) SetupSuite() {
	suite.chainID = factory.DefaultTestChainID
	suite.valSet, suite.privVals = types.RandValidatorSet(1)
	var err error
	suite.proTxHash, err = suite.privVals[0].GetProTxHash(context.Background())
	require.NoError(suite.T(), err)
}

func (suite *GossiperSuiteTest) TearDownTest() {
	mock.AssertExpectationsForObjects(suite.T(), suite.stateCh, suite.dataCh, suite.voteCh, suite.blockStore)
}

func (suite *GossiperSuiteTest) SetupTest() {
	logger := log.NewTestingLogger(suite.T())
	nodeID := types.NodeID("test-peer")
	suite.stateCh = &mocks2.Channel{}
	suite.dataCh = &mocks2.Channel{}
	suite.voteCh = &mocks2.Channel{}
	suite.ps = NewPeerState(logger, nodeID)
	suite.sender = &p2pMsgSender{
		logger: logger,
		ps:     suite.ps,
		chans: channelBundle{
			state: suite.stateCh,
			data:  suite.dataCh,
			vote:  suite.voteCh,
		},
	}
	suite.blockStore = &mocks.BlockStore{}
	suite.gossiper = &msgGossiper{
		logger:    logger,
		ps:        suite.ps,
		msgSender: suite.sender,
		blockStore: &blockRepository{
			BlockStore: suite.blockStore,
			logger:     logger,
		},
		optimistic: true,
	}
}

func (suite *GossiperSuiteTest) TestGossipVoteSetMaj23() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const (
		H100 = int64(100)
		R0   = int32(0)
	)
	roundStateH100R0 := cstypes.RoundState{
		Height: H100,
		Round:  R0,
	}
	prevoteVoteH100R0 := suite.makeSignedVote(100, 0, tmproto.PrevoteType)
	prevoteVoteH100R1 := suite.makeSignedVote(100, 1, tmproto.PrevoteType)
	precommitVoteH100R0 := suite.makeSignedVote(100, 0, tmproto.PrecommitType)
	precommitVoteH100R1 := suite.makeSignedVote(100, 1, tmproto.PrecommitType)
	commitBlockID := types.BlockID{
		Hash:          tmrand.Bytes(crypto.HashSize),
		PartSetHeader: types.PartSetHeader{},
	}
	commit := types.Commit{
		Height:  H100,
		Round:   2,
		BlockID: commitBlockID,
	}
	prsDef := cstypes.PeerRoundState{
		Height:             H100,
		ProposalPOLRound:   -1,
		CatchupCommitRound: -1,
	}
	testCases := []struct {
		rs     cstypes.RoundState
		prs    cstypes.PeerRoundState
		vote   *types.Vote
		want   *tmcons.VoteSetMaj23
		mockFn func()
	}{
		{
			// send prevote
			rs:   roundStateH100R0,
			prs:  prsDef,
			vote: prevoteVoteH100R0,
			want: newVoteSetMaj23(H100, R0, tmproto.PrevoteType, prevoteVoteH100R0.BlockID),
		},
		{
			// send ProposalPOL
			rs: roundStateH100R0,
			prs: cstypes.PeerRoundState{
				Height:             H100,
				ProposalPOLRound:   1,
				CatchupCommitRound: -1,
			},
			vote: prevoteVoteH100R1,
			want: newVoteSetMaj23(H100, R0, tmproto.PrevoteType, prevoteVoteH100R1.BlockID),
		},
		{
			// send precommit
			rs:   roundStateH100R0,
			prs:  prsDef,
			vote: precommitVoteH100R0,
			want: newVoteSetMaj23(H100, R0, tmproto.PrecommitType, precommitVoteH100R0.BlockID),
		},
		{
			// send precommit for catchup commit
			rs: roundStateH100R0,
			prs: cstypes.PeerRoundState{
				Height:             H100,
				ProposalPOLRound:   -1,
				CatchupCommitRound: 1,
			},
			vote: precommitVoteH100R1,
			want: newVoteSetMaj23(H100, 2, tmproto.PrecommitType, commit.BlockID),
			mockFn: func() {
				suite.blockStore.On("Height").Return(H100)
				suite.blockStore.On("Base").Return(int64(1))
				suite.blockStore.On("LoadSeenCommit").Once().Return(nil)
				suite.blockStore.On("LoadBlockCommit", H100).Once().Return(&commit)
			},
		},
		{
			// send precommit for catchup commit
			rs: roundStateH100R0,
			prs: cstypes.PeerRoundState{
				Height:             H100,
				ProposalPOLRound:   -1,
				CatchupCommitRound: 1,
			},
			vote: precommitVoteH100R1,
			want: newVoteSetMaj23(H100, 2, tmproto.PrecommitType, commit.BlockID),
			mockFn: func() {
				suite.blockStore.On("Height").Return(H100)
				suite.blockStore.On("Base").Return(int64(1))
				suite.blockStore.On("LoadSeenCommit").Once().Return(&commit)
			},
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("test-case #%d", i), func() {
			if tc.mockFn != nil {
				tc.mockFn()
			}
			tc.rs.Votes = cstypes.NewHeightVoteSet(suite.chainID, H100, suite.valSet)
			added, err := tc.rs.Votes.AddVote(tc.vote)
			require.True(suite.T(), added)
			require.NoError(suite.T(), err)
			want := p2p.Envelope{
				To:      suite.ps.peerID,
				Message: tc.want,
			}
			suite.stateCh.
				On("Send", ctx, want).
				Once().
				Return(nil)
			suite.gossiper.GossipVoteSetMaj23(ctx, tc.rs, &tc.prs)
		})
	}
}

func (suite *GossiperSuiteTest) TestGossipProposalBlockParts() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	commit := types.Commit{Height: 99, Round: 0}
	block := types.MakeBlock(100, types.Txs{[]byte{1, 2, 3}}, &commit, nil)
	block.Header.ValidatorsHash = tmrand.Bytes(crypto.HashSize)
	partSet, err := block.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(suite.T(), err)
	blockID := block.BlockID(nil)
	part0 := partSet.GetPart(0)
	protoPart0, err := part0.ToProto()
	require.NoError(suite.T(), err)
	testCases := []struct {
		rs       cstypes.RoundState
		prs      cstypes.PeerRoundState
		wantMsg  *tmcons.BlockPart
		wantPBPs int
	}{
		{
			rs: cstypes.RoundState{
				Height:             100,
				Round:              0,
				ProposalBlockParts: partSet,
			},
			prs: cstypes.PeerRoundState{
				Height:             100,
				Round:              0,
				ProposalBlockParts: types.NewPartSetFromHeader(blockID.PartSetHeader).BitArray(),
			},
			wantPBPs: 1,
			wantMsg: &tmcons.BlockPart{
				Height: 100,
				Round:  0,
				Part:   *protoPart0,
			},
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("test-case #%d", i), func() {
			suite.ps.PRS = tc.prs
			want := p2p.Envelope{
				To:      suite.ps.peerID,
				Message: tc.wantMsg,
			}
			suite.dataCh.
				On("Send", ctx, want).
				Once().
				Return(nil)
			suite.gossiper.GossipProposalBlockParts(ctx, tc.rs, &tc.prs)
			suite.Equal(tc.wantPBPs, tc.prs.ProposalBlockParts.Bits)
		})
	}
}

func (suite *GossiperSuiteTest) TestGossipProposal() {
	const (
		H100 = 100
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	blockID := factory.MakeBlockID()
	now := time.Now().UTC()
	proposalPOLRoundMinus1 := types.NewProposal(H100, 2400, 0, -1, blockID, now)
	proposalPOLRound1 := types.NewProposal(H100, 2400, 0, 1, blockID, now)
	prevoteVoteH100R1 := suite.makeSignedVote(H100, 1, tmproto.PrevoteType)
	prevoteVotes := cstypes.NewHeightVoteSet(suite.chainID, H100, suite.valSet)
	added, err := prevoteVotes.AddVote(prevoteVoteH100R1)
	require.True(suite.T(), added)
	require.NoError(suite.T(), err)
	testCases := []struct {
		rs       cstypes.RoundState
		prs      cstypes.PeerRoundState
		wantMsgs []proto.Message
	}{
		{
			rs: cstypes.RoundState{
				Height:   100,
				Proposal: proposalPOLRoundMinus1,
			},
			prs: cstypes.PeerRoundState{
				Height:   100,
				Round:    0,
				Proposal: false,
			},
			wantMsgs: []proto.Message{
				&tmcons.Proposal{
					Proposal: *proposalPOLRoundMinus1.ToProto(),
				},
			},
		},
		{
			rs: cstypes.RoundState{
				Height:   100,
				Proposal: proposalPOLRound1,
				Votes:    prevoteVotes,
			},
			prs: cstypes.PeerRoundState{
				Height:   100,
				Round:    0,
				Proposal: true,
			},
			wantMsgs: []proto.Message{
				&tmcons.Proposal{
					Proposal: *proposalPOLRound1.ToProto(),
				},
				&tmcons.ProposalPOL{
					Height:           100,
					ProposalPolRound: 1,
					ProposalPol:      *prevoteVotes.Prevotes(1).BitArray().ToProto(),
				},
			},
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("test-case #%d", i), func() {
			suite.ps.PRS = tc.prs
			for _, want := range tc.wantMsgs {
				suite.dataCh.
					On("Send", ctx, p2p.Envelope{To: suite.ps.peerID, Message: want}).
					Once().
					Return(nil)
			}
			suite.gossiper.GossipProposal(ctx, tc.rs, &tc.prs)
			newPRS := suite.gossiper.ps.GetRoundState()
			require.True(suite.T(), newPRS.Proposal)
		})
	}
}

func (suite *GossiperSuiteTest) makeVote(height int64, round int32, msgType tmproto.SignedMsgType) *types.Vote {
	randBytes := tmrand.Bytes(crypto.HashSize)
	return &types.Vote{
		ValidatorProTxHash: suite.proTxHash,
		ValidatorIndex:     0,
		Height:             height,
		Round:              round,
		Type:               msgType,
		BlockID: types.BlockID{
			Hash:          randBytes,
			PartSetHeader: types.PartSetHeader{},
		},
	}
}

func (suite *GossiperSuiteTest) makeSignedVote(height int64, round int32, msgType tmproto.SignedMsgType) *types.Vote {
	vote := suite.makeVote(height, round, msgType)
	suite.signVote(vote)
	return vote
}

func (suite *GossiperSuiteTest) signVote(vote *types.Vote) {
	ctx := context.Background()
	protoVote := vote.ToProto()
	privVal := suite.privVals[vote.ValidatorIndex]
	err := privVal.SignVote(ctx, suite.chainID, suite.valSet.QuorumType, suite.valSet.QuorumHash, protoVote, nil)
	require.NoError(suite.T(), err)
	vote.BlockSignature = protoVote.BlockSignature
	err = vote.VoteExtensions.CopySignsFromProto(protoVote.VoteExtensionsToMap())
	require.NoError(suite.T(), err)
}
