package consensus

import (
	"context"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/cosmos/gogoproto/proto"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/mock"
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
	clock      *clockwork.FakeClock
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
	suite.clock = clockwork.NewFakeClock()
	suite.gossiper = &msgGossiper{
		logger:    suite.logger,
		ps:        suite.ps,
		msgSender: suite.sender,
		blockStore: &blockRepository{
			BlockStore: suite.blockStore,
			logger:     suite.logger,
		},
		optimistic: true,
		clock:      suite.clock,
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
			// The cases are independent occasions at the same height; without this
			// they share one catch-up retry interval and only the first one acts.
			suite.clock.Advance(catchupResendInterval)
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

// TestGossipBlockPartsForCatchupResends verifies the two halves of the catch-up
// contract for a single-part block: the part is never optimistically recorded as
// delivered (a lagging peer may silently drop it, and marking would wedge the
// peer with an incomplete block), and it is replayed once the retry interval has
// elapsed rather than on every gossip tick.
func (suite *GossiperSuiteTest) TestGossipBlockPartsForCatchupResends() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	partSet := types.NewPartSetFromData(tmrand.Bytes(100), 100)
	suite.Require().Equal(uint32(1), partSet.Total())
	part0 := partSet.GetPart(0)
	protoPart0, err := part0.ToProto()
	suite.Require().NoError(err)
	blockMeta := types.BlockMeta{BlockID: types.BlockID{PartSetHeader: partSet.Header()}}

	// Peer is behind us at height 999 and has none of the parts yet.
	suite.ps.PRS = cstypes.PeerRoundState{
		Height:                     999,
		Round:                      0,
		ProposalBlockParts:         partSet.BitArray().Not(),
		ProposalBlockPartSetHeader: partSet.Header(),
	}

	suite.blockStore.On("LoadBlockMeta", int64(999)).Twice().Return(&blockMeta)
	suite.blockStore.On("LoadBlockPart", int64(999), 0).Twice().Return(part0)
	sends := 0
	suite.dataCh.
		On("Send", ctx, p2p.Envelope{
			To:      suite.ps.peerID,
			Message: &tmcons.BlockPart{Height: 999, Round: 0, Part: *protoPart0},
		}).
		Run(func(_ mock.Arguments) { sends++ }).
		Twice().
		Return(nil)

	// First tick sends the missing part, completing the pass for this height.
	suite.gossiper.GossipBlockPartsForCatchup(ctx, cstypes.RoundState{}, suite.ps.GetRoundState())
	suite.Require().Equal(1, sends)
	suite.Require().False(suite.ps.PRS.ProposalBlockParts.GetIndex(0),
		"catch-up must not optimistically mark the peer as having the part")

	// Further ticks inside the retry interval must not replay the part: that is
	// what floods a peer we only believe to be lagging.
	for range 100 {
		suite.gossiper.GossipBlockPartsForCatchup(ctx, cstypes.RoundState{}, suite.ps.GetRoundState())
	}
	suite.Require().Equal(1, sends, "catch-up must not resend within the retry interval")

	// Once the interval elapses the peer is still behind, so the part is resent.
	suite.clock.Advance(catchupResendInterval)
	suite.gossiper.GossipBlockPartsForCatchup(ctx, cstypes.RoundState{}, suite.ps.GetRoundState())
	suite.Require().Equal(2, sends, "catch-up must resend after the retry interval")
	suite.Require().False(suite.ps.PRS.ProposalBlockParts.GetIndex(0),
		"catch-up must not optimistically mark the peer as having the part")
}

// TestGossipBlockPartsForCatchupResendsMultiPart verifies that catch-up
// block-part gossip iterates across ALL missing indices in a multi-part block,
// not just a single index, while spending at most one part-set pass per retry
// interval. The peer's ProposalBlockParts bit-array must never be optimistically
// marked.
func (suite *GossiperSuiteTest) TestGossipBlockPartsForCatchupResendsMultiPart() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const partSize = uint32(100)
	// Build a block that serializes into exactly 3 parts.
	partSet := types.NewPartSetFromData(tmrand.Bytes(int(partSize)*3), partSize)
	suite.Require().Equal(uint32(3), partSet.Total(), "need a 3-part block for this test")
	blockMeta := types.BlockMeta{BlockID: types.BlockID{PartSetHeader: partSet.Header()}}

	// Peer is behind at height 999 and has received none of the three parts.
	suite.ps.PRS = cstypes.PeerRoundState{
		Height:                     999,
		Round:                      0,
		ProposalBlockParts:         partSet.BitArray().Not(), // peer has none
		ProposalBlockPartSetHeader: partSet.Header(),
	}

	// A pass spends one send per part, each picking a random missing index, so a
	// single pass need not cover every index. By the coupon-collector model 30
	// passes of 3 sends leave any index unseen with probability (2/3)^90 ≈
	// 10^-16 — negligible in practice.
	const passes = 30

	suite.blockStore.On("LoadBlockMeta", int64(999)).Return(&blockMeta)
	for i := uint32(0); i < partSet.Total(); i++ {
		suite.blockStore.On("LoadBlockPart", int64(999), int(i)).Return(partSet.GetPart(int(i)))
	}

	// Capture which part indices the gossiper actually sends.
	sentIndices := make(map[uint32]bool)
	sends := 0
	suite.dataCh.On("Send", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			env, ok := args.Get(1).(p2p.Envelope)
			if !ok {
				return
			}
			if bp, ok := env.Message.(*tmcons.BlockPart); ok {
				sentIndices[bp.Part.Index] = true
				sends++
			}
		}).
		Return(nil)

	// One pass covers the part set; extra ticks within the interval are skipped.
	for range int(partSet.Total()) + 5 {
		suite.gossiper.GossipBlockPartsForCatchup(ctx, cstypes.RoundState{}, suite.ps.GetRoundState())
	}
	suite.Require().Equal(int(partSet.Total()), sends,
		"a catch-up pass must send exactly one part per part-set entry")

	for range passes - 1 {
		suite.clock.Advance(catchupResendInterval)
		for range int(partSet.Total()) {
			suite.gossiper.GossipBlockPartsForCatchup(ctx, cstypes.RoundState{}, suite.ps.GetRoundState())
		}
	}
	suite.Require().Equal(passes*int(partSet.Total()), sends,
		"each elapsed retry interval must allow exactly one further pass")

	// Every part index must have been sent to the peer at least once.
	for i := uint32(0); i < partSet.Total(); i++ {
		suite.Assert().True(sentIndices[i],
			"part index %d was never sent across %d catch-up passes", i, passes)
	}

	// The peer's bit-array must remain un-marked (no optimistic delivery).
	prs := suite.ps.GetRoundState()
	for i := uint32(0); i < partSet.Total(); i++ {
		suite.Assert().False(prs.ProposalBlockParts.GetIndex(int(i)),
			"catch-up must not optimistically mark part %d as delivered", i)
	}
}

// TestGossipBlockPartsForCatchupBudgetsMissingParts pins the catch-up pass to
// the number of parts the peer is actually missing rather than the size of its
// bit-array. The peer supplies that bit-array over the wire and only its length
// is validated, so budgeting on the length would let a peer reporting a single
// missing part draw one resend of it per bit in the array.
func (suite *GossiperSuiteTest) TestGossipBlockPartsForCatchupBudgetsMissingParts() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const partSize = uint32(100)
	partSet := types.NewPartSetFromData(tmrand.Bytes(int(partSize)*3), partSize)
	suite.Require().Equal(uint32(3), partSet.Total(), "need a 3-part block for this test")
	blockMeta := types.BlockMeta{BlockID: types.BlockID{PartSetHeader: partSet.Header()}}

	// The peer reports every part but the last, so exactly one is missing.
	peerParts := partSet.BitArray().Copy()
	peerParts.SetIndex(2, false)
	suite.Require().Equal(2, peerParts.CountTrueBits())

	suite.ps.PRS = cstypes.PeerRoundState{
		Height:                     999,
		Round:                      0,
		ProposalBlockParts:         peerParts,
		ProposalBlockPartSetHeader: partSet.Header(),
	}

	suite.blockStore.On("LoadBlockMeta", int64(999)).Return(&blockMeta)
	suite.blockStore.On("LoadBlockPart", int64(999), 2).Return(partSet.GetPart(2))

	sends := 0
	suite.dataCh.On("Send", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			env, ok := args.Get(1).(p2p.Envelope)
			if !ok {
				return
			}
			if _, ok := env.Message.(*tmcons.BlockPart); ok {
				sends++
			}
		}).
		Return(nil)

	for range int(partSet.Total()) + 5 {
		suite.gossiper.GossipBlockPartsForCatchup(ctx, cstypes.RoundState{}, suite.ps.GetRoundState())
	}
	suite.Require().Equal(1, sends,
		"a pass must spend one send per missing part, not one per bit-array entry")

	suite.clock.Advance(catchupResendInterval)
	suite.gossiper.GossipBlockPartsForCatchup(ctx, cstypes.RoundState{}, suite.ps.GetRoundState())
	suite.Require().Equal(2, sends, "the missing part is retried once the interval elapses")
}

// TestGossipBlockPartsForCatchupPeerHasEveryPart covers the peer reporting a
// complete part set: there is nothing to send, and no pass may be opened.
func (suite *GossiperSuiteTest) TestGossipBlockPartsForCatchupPeerHasEveryPart() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	partSet := types.NewPartSetFromData(tmrand.Bytes(100), 100)
	suite.ps.PRS = cstypes.PeerRoundState{
		Height:                     999,
		Round:                      0,
		ProposalBlockParts:         partSet.BitArray(), // peer has them all
		ProposalBlockPartSetHeader: partSet.Header(),
	}

	// Neither the block store nor the data channel may be touched; the mocks are
	// constructed with no expectations, so any call fails the test.
	for range 10 {
		suite.gossiper.GossipBlockPartsForCatchup(ctx, cstypes.RoundState{}, suite.ps.GetRoundState())
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
