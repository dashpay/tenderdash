package consensus

import (
	"context"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/suite"

	"github.com/dashpay/tenderdash/crypto"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/internal/p2p"
	p2pmocks "github.com/dashpay/tenderdash/internal/p2p/mocks"
	"github.com/dashpay/tenderdash/internal/state/mocks"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/log"
	tmrand "github.com/dashpay/tenderdash/libs/rand"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

type GossiperSuiteTest struct {
	suite.Suite

	ps         *PeerState
	gossiper   *msgGossiper
	sender     *p2pMsgSender
	blockStore *mocks.BlockStore
	stateCh    *p2pmocks.Channel
	dataCh     *p2pmocks.Channel
	voteCh     *p2pmocks.Channel
	proTxHash  types.ProTxHash
	valSet     *types.ValidatorSet
	privVals   []types.PrivValidator
	logger     *log.TestingLogger
}

func TestGossiper(t *testing.T) {
	suite.Run(t, new(GossiperSuiteTest))
}

func (suite *GossiperSuiteTest) SetupSuite() {
	suite.valSet, suite.privVals = types.RandValidatorSet(1)
	var err error
	suite.proTxHash, err = suite.privVals[0].GetProTxHash(context.Background())
	suite.Require().NoError(err)
}

func (suite *GossiperSuiteTest) SetupTest() {
	suite.logger = log.NewTestingLogger(suite.T())
	nodeID := types.NodeID("test-peer")
	suite.stateCh = p2pmocks.NewChannel(suite.T())
	suite.dataCh = p2pmocks.NewChannel(suite.T())
	suite.voteCh = p2pmocks.NewChannel(suite.T())
	suite.ps = NewPeerState(suite.logger, nodeID)
	suite.sender = &p2pMsgSender{
		logger: suite.logger,
		ps:     suite.ps,
		chans: channelBundle{
			state: suite.stateCh,
			data:  suite.dataCh,
			vote:  suite.voteCh,
		},
	}
	suite.blockStore = &mocks.BlockStore{}
	suite.gossiper = &msgGossiper{
		logger:    suite.logger,
		ps:        suite.ps,
		msgSender: suite.sender,
		blockStore: &blockRepository{
			BlockStore: suite.blockStore,
			logger:     suite.logger,
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
		suite.Run(fmt.Sprintf("%d", i), func() {
			if tc.mockFn != nil {
				tc.mockFn()
			}
			tc.rs.Votes = cstypes.NewHeightVoteSet(factory.DefaultTestChainID, H100, suite.valSet)
			added, err := tc.rs.Votes.AddVote(tc.vote)
			suite.Require().True(added)
			suite.Require().NoError(err)
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
	suite.Require().NoError(err)
	blockID := block.BlockID(nil)
	part0 := partSet.GetPart(0)
	protoPart0, err := part0.ToProto()
	suite.Require().NoError(err)
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
		suite.Run(fmt.Sprintf("%d", i), func() {
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
	prevoteVotes := cstypes.NewHeightVoteSet(factory.DefaultTestChainID, H100, suite.valSet)
	added, err := prevoteVotes.AddVote(prevoteVoteH100R1)
	suite.Require().True(added)
	suite.Require().NoError(err)
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
		suite.Run(fmt.Sprintf("%d", i), func() {
			suite.ps.PRS = tc.prs
			for _, want := range tc.wantMsgs {
				suite.dataCh.
					On("Send", ctx, p2p.Envelope{To: suite.ps.peerID, Message: want}).
					Once().
					Return(nil)
			}
			suite.gossiper.GossipProposal(ctx, tc.rs, &tc.prs)
			newPRS := suite.gossiper.ps.GetRoundState()
			suite.Require().True(newPRS.Proposal)
		})
	}
}

func (suite *GossiperSuiteTest) TestGossipBlockPartsForCatchup() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	partSet1 := types.NewPartSetFromData(tmrand.Bytes(100), 100)
	part0 := partSet1.GetPart(0)
	protoPart0, err := part0.ToProto()
	suite.Require().NoError(err)
	blockMeta := types.BlockMeta{BlockID: types.BlockID{PartSetHeader: partSet1.Header()}}

	partSet2 := types.NewPartSetFromData(tmrand.Bytes(100), 100)
	blockMeta2 := types.BlockMeta{BlockID: types.BlockID{PartSetHeader: partSet2.Header()}}

	testCases := []struct {
		prs     cstypes.PeerRoundState
		wantMsg *tmcons.BlockPart
		mockFn  func()
		wantLog string
	}{
		{
			prs: cstypes.PeerRoundState{
				Height:                     999,
				ProposalBlockParts:         partSet1.BitArray().Not(),
				ProposalBlockPartSetHeader: partSet1.Header(),
			},
			mockFn: func() {
				suite.blockStore.On("LoadBlockMeta", int64(999)).Once().Return(&blockMeta)
				suite.blockStore.On("LoadBlockPart", int64(999), 0).Once().Return(part0)
			},
			wantMsg: &tmcons.BlockPart{Height: 999, Round: 0, Part: *protoPart0},
		},
		{
			prs: cstypes.PeerRoundState{Height: 999, ProposalBlockParts: partSet1.BitArray().Not()},
			mockFn: func() {
				suite.blockStore.On("LoadBlockMeta", int64(999)).Once().Return(nil)
				suite.blockStore.On("Base").Once().Return(int64(1))
				suite.blockStore.On("Height").Once().Return(int64(1000))
			},
			wantLog: `couldn't find a block meta`,
		},
		{
			prs: cstypes.PeerRoundState{Height: 999, ProposalBlockParts: partSet1.BitArray().Not()},
			mockFn: func() {
				suite.blockStore.On("LoadBlockMeta", int64(999)).Once().Return(&blockMeta2)
			},
			wantLog: `block and peer part-set headers do not match`,
		},
		{
			prs: cstypes.PeerRoundState{Height: 999,
				ProposalBlockParts:         partSet1.BitArray().Not(),
				ProposalBlockPartSetHeader: partSet1.Header(),
			},
			mockFn: func() {
				suite.blockStore.On("LoadBlockMeta", int64(999)).Once().Return(&blockMeta)
				suite.blockStore.On("LoadBlockPart", int64(999), 0).Once().Return(nil)
			},
			wantLog: `couldn't find a block part`,
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("%d", i), func() {
			if tc.mockFn != nil {
				tc.mockFn()
			}
			suite.ps.PRS = tc.prs
			if tc.wantMsg != nil {
				suite.dataCh.
					On("Send", ctx, p2p.Envelope{To: suite.ps.peerID, Message: tc.wantMsg}).
					Once().
					Return(nil)
			}
			if tc.wantLog != "" {
				suite.logger.AssertMatch(regexp.MustCompile(tc.wantLog))
			}
			suite.gossiper.GossipBlockPartsForCatchup(ctx, cstypes.RoundState{}, &tc.prs)
		})
	}
}

func (suite *GossiperSuiteTest) TestGossipCommit() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	suite.blockStore.On("Base").Return(int64(100))
	commitH1000 := types.Commit{Height: 1000, Round: 0}
	commitH998 := types.Commit{Height: 998, Round: 0}
	testCases := []struct {
		rs      cstypes.RoundState
		prs     cstypes.PeerRoundState
		wantMsg *tmproto.Commit
		mockFn  func()
	}{
		{
			rs: cstypes.RoundState{Height: 1000, LastCommit: &commitH1000},
			prs: cstypes.PeerRoundState{
				Height:    999,
				HasCommit: false,
			},
			wantMsg: commitH1000.ToProto(),
		},
		{
			rs: cstypes.RoundState{Height: 1000},
			prs: cstypes.PeerRoundState{
				Height:    998,
				HasCommit: false,
			},
			mockFn: func() {
				suite.blockStore.On("LoadBlockCommit", int64(998)).Once().Return(&commitH998)
			},
			wantMsg: commitH998.ToProto(),
		},
		{
			rs: cstypes.RoundState{Height: 1000},
			prs: cstypes.PeerRoundState{
				Height:    1000,
				HasCommit: false,
			},
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("%d", i), func() {
			if tc.mockFn != nil {
				tc.mockFn()
			}
			suite.ps.PRS = tc.prs
			shouldSendHasCommit := false
			if tc.wantMsg != nil {
				shouldSendHasCommit = true
				msg := &tmcons.Commit{Commit: tc.wantMsg}
				suite.voteCh.
					On("Send", ctx, p2p.Envelope{To: suite.ps.peerID, Message: msg}).
					Once().
					Return(nil)
			}
			suite.gossiper.GossipCommit(ctx, tc.rs, &tc.prs)
			newPRS := suite.gossiper.ps.GetRoundState()
			suite.Require().Equal(shouldSendHasCommit, newPRS.HasCommit)
		})
	}
}

func (suite *GossiperSuiteTest) TestGossipGossipVote() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	prevoteH100R0 := suite.makeSignedVote(100, 0, tmproto.PrevoteType)
	prevoteH100R1 := suite.makeSignedVote(100, 1, tmproto.PrevoteType)
	prevoteH100R2 := suite.makeSignedVote(100, 2, tmproto.PrevoteType)
	precommitH100R0 := suite.makeSignedVote(100, 0, tmproto.PrecommitType)
	votesH100 := cstypes.NewHeightVoteSet(factory.DefaultTestChainID, 100, suite.valSet)
	_, _ = votesH100.AddVote(prevoteH100R0)
	_, _ = votesH100.AddVote(prevoteH100R1)
	_, _ = votesH100.AddVote(prevoteH100R2)
	_, _ = votesH100.AddVote(precommitH100R0)

	testCases := []struct {
		rs      cstypes.RoundState
		prs     cstypes.PeerRoundState
		wantMsg *tmproto.Vote
	}{
		{
			rs: cstypes.RoundState{Votes: votesH100},
			prs: cstypes.PeerRoundState{
				Height:           100,
				Round:            0,
				ProposalPOLRound: 0,
				Step:             cstypes.RoundStepNewHeight,
			},
			wantMsg: prevoteH100R0.ToProto(),
		},
		{
			rs: cstypes.RoundState{Votes: votesH100, Round: 0},
			prs: cstypes.PeerRoundState{
				Height:           100,
				Round:            0,
				ProposalPOLRound: 0,
				Step:             cstypes.RoundStepPropose,
			},
			wantMsg: prevoteH100R0.ToProto(),
		},
		{
			rs: cstypes.RoundState{Votes: votesH100, Round: 1},
			prs: cstypes.PeerRoundState{
				Height:           100,
				Round:            1,
				ProposalPOLRound: 0,
				Step:             cstypes.RoundStepPrevoteWait,
			},
			wantMsg: prevoteH100R1.ToProto(),
		},
		{
			rs: cstypes.RoundState{Votes: votesH100, Round: 0},
			prs: cstypes.PeerRoundState{
				Height:           100,
				Round:            0,
				ProposalPOLRound: 0,
				Step:             cstypes.RoundStepPrecommitWait,
			},
			wantMsg: precommitH100R0.ToProto(),
		},
		{
			rs: cstypes.RoundState{Votes: votesH100, Round: 3},
			prs: cstypes.PeerRoundState{
				Height:           100,
				Round:            2,
				ProposalPOLRound: 2,
				Step:             cstypes.RoundStepPrevote,
			},
			wantMsg: prevoteH100R2.ToProto(),
		},
		{
			rs: cstypes.RoundState{Votes: votesH100, Round: 3},
			prs: cstypes.PeerRoundState{
				Height:           100,
				Round:            2,
				ProposalPOLRound: 2,
				Step:             cstypes.RoundStepPrevote,
			},
			wantMsg: prevoteH100R2.ToProto(),
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("%d", i), func() {
			suite.ps.PRS = tc.prs
			if tc.wantMsg != nil {
				msg := &tmcons.Vote{Vote: tc.wantMsg}
				suite.voteCh.
					On("Send", ctx, p2p.Envelope{To: suite.ps.peerID, Message: msg}).
					Once().
					Return(nil)
			}
			suite.gossiper.GossipVote(ctx, tc.rs, &tc.prs)
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
	err := privVal.SignVote(ctx, factory.DefaultTestChainID, suite.valSet.QuorumType, suite.valSet.QuorumHash, protoVote, nil)
	suite.Require().NoError(err)
	vote.BlockSignature = protoVote.BlockSignature
	err = vote.VoteExtensions.CopySignsFromProto(protoVote.VoteExtensions)
	suite.Require().NoError(err)
}
