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
	"github.com/dashpay/tenderdash/libs/bits"
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
			wantLog: `failed to load block meta`,
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
			wantLog: `failed to load block part`,
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

// TestGossipBlockPartsForCatchupBudgetIsNeverRaisedMidPass pins a pass's
// budget as an upper bound fixed at open: it may be clamped down when the
// peer's reported missing count drops (see
// TestGossipBlockPartsForCatchupBudgetShrinksWithReportedMissing) but never
// raised back up once clamped. The peer supplies the bit-array the missing
// count is derived from and may replace it between ticks, so if raising the
// count mid-pass were honored, a peer could both recover budget the clamp had
// taken away and step over the deadline the clamp-triggered exhaustion armed.
func (suite *GossiperSuiteTest) TestGossipBlockPartsForCatchupBudgetIsNeverRaisedMidPass() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const partSize = uint32(100)
	const numParts = 5
	partSet := types.NewPartSetFromData(tmrand.Bytes(int(partSize)*numParts), partSize)
	suite.Require().Equal(uint32(numParts), partSet.Total(), "need headroom above the clamped-down budget")
	blockMeta := types.BlockMeta{BlockID: types.BlockID{PartSetHeader: partSet.Header()}}

	// reportMissing has the peer claim its first n parts are outstanding.
	reportMissing := func(n int) {
		reported := partSet.BitArray().Copy() // every bit set: peer has them all
		for i := range n {
			reported.SetIndex(i, false)
		}
		suite.Require().Equal(n, reported.Not().CountTrueBits())
		suite.ps.PRS.ProposalBlockParts = reported
	}

	suite.ps.PRS = cstypes.PeerRoundState{
		Height:                     999,
		Round:                      0,
		ProposalBlockPartSetHeader: partSet.Header(),
	}
	reportMissing(numParts)

	suite.blockStore.On("LoadBlockMeta", int64(999)).Return(&blockMeta)
	for i := range int(partSet.Total()) {
		suite.blockStore.On("LoadBlockPart", int64(999), i).Return(partSet.GetPart(i))
	}

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

	tick := func() {
		suite.gossiper.GossipBlockPartsForCatchup(ctx, cstypes.RoundState{}, suite.ps.GetRoundState())
	}

	// Open a 5-send pass and spend two of them, leaving 3 in the budget.
	tick()
	tick()

	// Drop hard, to fewer than the 3 remaining: a genuine clamp, not a no-op.
	// The pass now has only 1 slot left instead of 3, so it exhausts (and arms
	// the deadline) on the very next tick.
	reportMissing(1)
	tick()
	suite.Require().Equal(3, sends, "the clamp must actually shrink the remaining budget, ending the pass early")

	// Raise it back to the original count, at the same clock instant the
	// deadline was just armed at.
	reportMissing(numParts)
	tick()
	tick()
	suite.Require().Equal(3, sends,
		"raising the reported count after the clamp ended the pass must not reopen it before the deadline")

	// Only once the interval elapses does a fresh pass open, budgeted on
	// whatever the peer currently reports -- all 5 parts once again.
	suite.clock.Advance(catchupResendInterval)
	tick()
	suite.Require().Equal(4, sends, "the next pass opens fresh, unrelated to the one the clamp cut short")
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

// TestGossipBlockPartsForCatchupRetryIntervalArmsWhenPassEnds verifies the
// pass's retry deadline is measured from its last send, not from when it
// opened. A pass with enough missing parts to span more gossip ticks than
// catchupResendInterval allows takes longer than the interval to exhaust; a
// deadline armed at open time has already elapsed by then, so the next tick
// would otherwise reopen a fresh pass immediately - leaving no quiet gap
// between passes, which is exactly the flooding behavior this throttle
// exists to stop.
func (suite *GossiperSuiteTest) TestGossipBlockPartsForCatchupRetryIntervalArmsWhenPassEnds() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const partSize = uint32(100)
	const numParts = 6
	partSet := types.NewPartSetFromData(tmrand.Bytes(int(partSize)*numParts), partSize)
	suite.Require().Equal(uint32(numParts), partSet.Total(), "need a 6-part block for this test")
	blockMeta := types.BlockMeta{BlockID: types.BlockID{PartSetHeader: partSet.Header()}}

	suite.ps.PRS = cstypes.PeerRoundState{
		Height:                     999,
		Round:                      0,
		ProposalBlockParts:         partSet.BitArray().Not(), // peer has none
		ProposalBlockPartSetHeader: partSet.Header(),
	}

	suite.blockStore.On("LoadBlockMeta", int64(999)).Return(&blockMeta)
	for i := uint32(0); i < partSet.Total(); i++ {
		suite.blockStore.On("LoadBlockPart", int64(999), int(i)).Return(partSet.GetPart(int(i)))
	}

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

	tick := func() {
		suite.gossiper.GossipBlockPartsForCatchup(ctx, cstypes.RoundState{}, suite.ps.GetRoundState())
	}

	// A gossip tick every fifth of the interval: 6 missing parts therefore
	// take longer than catchupResendInterval to exhaust the pass.
	step := catchupResendInterval / 5

	// The first 5 ticks drain 5 of the pass's 6 sends, ending at
	// t = catchupResendInterval.
	for range 5 {
		tick()
		suite.clock.Advance(step)
	}
	suite.Require().Equal(5, sends)

	// This tick, still at t = catchupResendInterval, spends the pass's last
	// send.
	tick()
	suite.Require().Equal(6, sends, "the pass's budget covers all 6 missing parts")

	// A tick right after the pass's last send must NOT open a new pass: a
	// full interval must elapse after the LAST send, not after the pass
	// opened (which was catchupResendInterval ago by now too).
	tick()
	suite.Require().Equal(6, sends,
		"no further send until a full retry interval has elapsed since the pass's last send")

	// Advancing to just short of a full interval past the last send still
	// must not reopen the pass.
	suite.clock.Advance(catchupResendInterval - step)
	tick()
	suite.Require().Equal(6, sends,
		"the retry deadline must be measured from the pass's last send, not from when it opened")

	// Only once a full interval has elapsed since the last send does a fresh
	// pass open.
	suite.clock.Advance(step)
	tick()
	suite.Require().Equal(7, sends, "a fresh pass opens once the full interval has elapsed")
}

// TestGossipBlockPartsForCatchupHeightAdvanceInterruptingPassIsThrottled is a
// regression test for a defect where a height change interrupting a pass
// BEFORE it exhausted its own budget left catchupRetryAt unarmed (only
// exhaustion or a failed send armed it). A peer that advances its reported
// height every tick -- legitimate and monotonic, no protocol violation -- then
// forced a fresh full-budget pass on every single tick, forever, whenever the
// block at each claimed height took more than one tick to exhaust (i.e. any
// block with more than one missing part, the common case). The clock is never
// advanced across the first block of ticks below: a working throttle must
// still bound the send rate even though the peer supplies a "new" pass at
// every step.
func (suite *GossiperSuiteTest) TestGossipBlockPartsForCatchupHeightAdvanceInterruptingPassIsThrottled() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const partSize = uint32(100)
	const numParts = 3
	const numHeights = 20

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

	tick := func(height int64) {
		partSet := types.NewPartSetFromData(tmrand.Bytes(int(partSize)*numParts), partSize)
		suite.Require().Equal(uint32(numParts), partSet.Total())
		blockMeta := types.BlockMeta{BlockID: types.BlockID{PartSetHeader: partSet.Header()}}
		suite.blockStore.On("LoadBlockMeta", height).Return(&blockMeta)
		for i := uint32(0); i < partSet.Total(); i++ {
			suite.blockStore.On("LoadBlockPart", height, int(i)).Return(partSet.GetPart(int(i)))
		}
		suite.ps.PRS = cstypes.PeerRoundState{
			Height:                     height,
			Round:                      0,
			ProposalBlockParts:         partSet.BitArray().Not(), // peer has none
			ProposalBlockPartSetHeader: partSet.Header(),
		}
		suite.gossiper.GossipBlockPartsForCatchup(ctx, cstypes.RoundState{}, suite.ps.GetRoundState())
	}

	// The peer advances its reported height on every tick, at the same clock
	// instant, before any pass at a prior height could exhaust its 3-send
	// budget. A throttle that only arms its deadline on exhaustion or failure
	// never gets a chance to arm it here.
	for h := int64(1); h <= numHeights; h++ {
		tick(h)
	}
	suite.Require().Equal(1, sends,
		"an unexhausted pass interrupted by a height change must still throttle to one pass per interval")

	suite.clock.Advance(catchupResendInterval)
	tick(numHeights + 1)
	suite.Require().Equal(2, sends, "a fresh pass opens once the interval has elapsed since the interrupted pass")
}

// TestGossipBlockPartsForCatchupBudgetShrinksWithReportedMissing verifies the
// pass's remaining budget is clamped down when the peer's bit-array later
// reports fewer parts missing than when the pass opened. Successful catch-up
// sends never mark parts delivered locally (see
// TestGossipBlockPartsForCatchupResends), so the peer fully controls what
// ProposalBlockParts.Not().CountTrueBits() reports on every tick -
// NewValidBlockMessage.ValidateBasic checks only the array's length and
// ApplyNewValidBlockMessage installs it wholesale. A peer that opens a pass
// reporting many parts missing and then swaps in a bit-array with only one
// unset bit must not keep the larger original budget: every remaining
// attempt would draw that same single index, funding repeated duplicate
// sends of it.
func (suite *GossiperSuiteTest) TestGossipBlockPartsForCatchupBudgetShrinksWithReportedMissing() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const partSize = uint32(100)
	const numParts = 5
	partSet := types.NewPartSetFromData(tmrand.Bytes(int(partSize)*numParts), partSize)
	suite.Require().Equal(uint32(numParts), partSet.Total(), "need a 5-part block for this test")
	blockMeta := types.BlockMeta{BlockID: types.BlockID{PartSetHeader: partSet.Header()}}

	suite.ps.PRS = cstypes.PeerRoundState{
		Height:                     999,
		Round:                      0,
		ProposalBlockParts:         partSet.BitArray().Not(), // peer has none: 5 missing
		ProposalBlockPartSetHeader: partSet.Header(),
	}

	suite.blockStore.On("LoadBlockMeta", int64(999)).Return(&blockMeta)
	for i := uint32(0); i < partSet.Total(); i++ {
		suite.blockStore.On("LoadBlockPart", int64(999), int(i)).Return(partSet.GetPart(int(i)))
	}

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

	tick := func() {
		suite.gossiper.GossipBlockPartsForCatchup(ctx, cstypes.RoundState{}, suite.ps.GetRoundState())
	}

	// The first two ticks open a 5-send pass and spend two of them.
	tick()
	tick()
	suite.Require().Equal(2, sends)

	// The peer now reports only one part missing, without the height
	// changing - a bit-array the peer fully controls over the wire (see the
	// doc comment above).
	shrunk := partSet.BitArray().Copy() // every bit set: peer has them all
	shrunk.SetIndex(4, false)           // except part 4
	suite.ps.PRS.ProposalBlockParts = shrunk
	suite.Require().Equal(1, shrunk.Not().CountTrueBits())

	for range 5 {
		tick()
	}
	suite.Require().Equal(3, sends,
		"a pass must not fund more sends than the peer's currently-reported missing count once it shrinks")

	// The next interval opens a fresh pass, budgeted on what the peer reports
	// at that point (still 1 missing).
	suite.clock.Advance(catchupResendInterval)
	tick()
	suite.Require().Equal(4, sends)
}

// TestGossipBlockPartsForCatchupCompleteReportMidPassClosesIt verifies that a
// peer reporting a complete part set while a pass is still open (budget not
// yet exhausted) closes the pass and arms the retry deadline, rather than
// leaving it open indefinitely. This is not about exceeding the pass's
// budget - catchupRemaining only ever counts down, so total sends per pass
// are the same either way. It's about what an indefinitely-open pass costs:
// the bit-array scan above runs on every tick instead of being skipped by
// backoff (see TestGossipBlockPartsForCatchupRetryIntervalArmsWhenPassEnds'
// sibling optimization), and the next report of missing parts resumes
// sending on the very same tick instead of after a fresh
// catchupResendInterval, which this test pins.
func (suite *GossiperSuiteTest) TestGossipBlockPartsForCatchupCompleteReportMidPassClosesIt() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const partSize = uint32(100)
	const numParts = 3
	partSet := types.NewPartSetFromData(tmrand.Bytes(int(partSize)*numParts), partSize)
	suite.Require().Equal(uint32(numParts), partSet.Total(), "need a 3-part block for this test")
	blockMeta := types.BlockMeta{BlockID: types.BlockID{PartSetHeader: partSet.Header()}}

	suite.ps.PRS = cstypes.PeerRoundState{
		Height:                     999,
		Round:                      0,
		ProposalBlockParts:         partSet.BitArray().Not(), // peer has none: 3 missing
		ProposalBlockPartSetHeader: partSet.Header(),
	}

	suite.blockStore.On("LoadBlockMeta", int64(999)).Return(&blockMeta)
	for i := uint32(0); i < partSet.Total(); i++ {
		suite.blockStore.On("LoadBlockPart", int64(999), int(i)).Return(partSet.GetPart(int(i)))
	}

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

	tick := func() {
		suite.gossiper.GossipBlockPartsForCatchup(ctx, cstypes.RoundState{}, suite.ps.GetRoundState())
	}

	// Open a 3-send pass and spend one of them.
	tick()
	suite.Require().Equal(1, sends)

	// The peer now reports a complete part set, without the height changing -
	// a bit-array the peer fully controls over the wire.
	suite.ps.PRS.ProposalBlockParts = partSet.BitArray() // every bit set: peer has them all
	tick()
	suite.Require().Equal(1, sends, "a complete report must not itself send")

	// The peer reports one part missing again, immediately (same simulated
	// instant - the clock has not advanced at all since the pass opened).
	suite.ps.PRS.ProposalBlockParts = partSet.BitArray().Not()
	tick()
	suite.Require().Equal(1, sends,
		"a pass closed by a complete report must honor the retry interval before sending again")

	// Only once the interval has elapsed does a fresh pass open.
	suite.clock.Advance(catchupResendInterval)
	tick()
	suite.Require().Equal(2, sends, "a fresh pass opens once the retry interval has elapsed")
}

// TestGossipBlockPartsForCatchupMismatchedHeaderEndsPass covers a pass that can
// never produce a send. The peer supplies both the part-set header and the size
// of the missing bit-array, and only their encoding is validated, so a pass
// opened on a maximum-size array with a header matching no stored block would
// otherwise charge a block-store read and an error log to every gossip tick for
// the whole budget - and, its deadline having been armed when the pass opened,
// reopen immediately afterwards.
func (suite *GossiperSuiteTest) TestGossipBlockPartsForCatchupMismatchedHeaderEndsPass() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stored := types.NewPartSetFromData(tmrand.Bytes(100), 100)
	blockMeta := types.BlockMeta{BlockID: types.BlockID{PartSetHeader: stored.Header()}}

	// Largest pass the peer can ask for: every bit of a maximum-length array
	// reported missing, under a header no stored block can match.
	bogus := types.NewPartSetFromData(tmrand.Bytes(100), 100)
	suite.Require().False(bogus.Header().Equals(stored.Header()))
	suite.ps.PRS = cstypes.PeerRoundState{
		Height:                     999,
		Round:                      0,
		ProposalBlockParts:         bits.NewBitArray(int(types.MaxBlockPartsCount)),
		ProposalBlockPartSetHeader: bogus.Header(),
	}

	// The data channel has no expectations: a mismatched header must send nothing.
	metaReads := 0
	suite.blockStore.On("LoadBlockMeta", int64(999)).
		Run(func(_ mock.Arguments) { metaReads++ }).
		Return(&blockMeta)

	for range 50 {
		suite.gossiper.GossipBlockPartsForCatchup(ctx, cstypes.RoundState{}, suite.ps.GetRoundState())
	}
	suite.Require().Equal(1, metaReads,
		"a pass that cannot send must cost one attempt per interval, not one per tick")

	suite.clock.Advance(catchupResendInterval)
	for range 50 {
		suite.gossiper.GossipBlockPartsForCatchup(ctx, cstypes.RoundState{}, suite.ps.GetRoundState())
	}
	suite.Require().Equal(2, metaReads, "each elapsed interval allows exactly one further attempt")
}

// TestGossipBlockPartsForCatchupSendFailureEndsPass is a regression test for a
// defect where syncProposalBlockPart unconditionally returned nil regardless of
// the underlying send's real result, so sendCatchupBlockPart always reported
// success: a send that never reached the peer was indistinguishable from one
// that did, and the pass kept spending its budget one failed attempt per tick
// instead of ending on the first one -- exactly the spin endCatchupPass exists
// to prevent. Uses a multi-part block so the budget itself (rather than
// endCatchupPass) is never the thing limiting attempts.
func (suite *GossiperSuiteTest) TestGossipBlockPartsForCatchupSendFailureEndsPass() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const partSize = uint32(100)
	partSet := types.NewPartSetFromData(tmrand.Bytes(int(partSize)*5), partSize)
	suite.Require().Equal(uint32(5), partSet.Total(), "need multiple missing parts so the budget isn't the limiting factor")
	blockMeta := types.BlockMeta{BlockID: types.BlockID{PartSetHeader: partSet.Header()}}

	suite.ps.PRS = cstypes.PeerRoundState{
		Height:                     999,
		Round:                      0,
		ProposalBlockParts:         partSet.BitArray().Not(), // peer has none
		ProposalBlockPartSetHeader: partSet.Header(),
	}

	suite.blockStore.On("LoadBlockMeta", int64(999)).Return(&blockMeta)
	partReads := 0
	suite.blockStore.On("LoadBlockPart", int64(999), mock.Anything).
		Run(func(_ mock.Arguments) { partReads++ }).
		Return(partSet.GetPart(0))

	// Every send fails to reach the peer.
	suite.dataCh.On("Send", mock.Anything, mock.Anything).Return(fmt.Errorf("simulated send failure"))

	for range 50 {
		suite.gossiper.GossipBlockPartsForCatchup(ctx, cstypes.RoundState{}, suite.ps.GetRoundState())
	}
	suite.Require().Equal(1, partReads,
		"a send that never reaches the peer must end the pass on the first attempt, not spend the whole budget")

	suite.clock.Advance(catchupResendInterval)
	for range 50 {
		suite.gossiper.GossipBlockPartsForCatchup(ctx, cstypes.RoundState{}, suite.ps.GetRoundState())
	}
	suite.Require().Equal(2, partReads, "each elapsed interval allows exactly one further attempt")
}

// TestGossipBlockPartsForCatchupMalformedHeaderDoesNotFundSends pins the budget
// to a pass that got as far as sending. A pass is deliberately not re-derived
// from the peer's bit-array once open, so a budget opened against an unusable
// header would otherwise stay spendable when the peer replaces its state, at the
// same height, with the stored block's real header and a single missing part:
// the whole inflated budget then lands as duplicate sends of that one part.
func (suite *GossiperSuiteTest) TestGossipBlockPartsForCatchupMalformedHeaderDoesNotFundSends() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	partSet := types.NewPartSetFromData(tmrand.Bytes(100), 100)
	suite.Require().Equal(uint32(1), partSet.Total())
	blockMeta := types.BlockMeta{BlockID: types.BlockID{PartSetHeader: partSet.Header()}}

	bogus := types.NewPartSetFromData(tmrand.Bytes(100), 100)
	suite.Require().False(bogus.Header().Equals(partSet.Header()))

	// Open the largest pass available, under a header that cannot match.
	suite.ps.PRS = cstypes.PeerRoundState{
		Height:                     999,
		Round:                      0,
		ProposalBlockParts:         bits.NewBitArray(int(types.MaxBlockPartsCount)),
		ProposalBlockPartSetHeader: bogus.Header(),
	}

	suite.blockStore.On("LoadBlockMeta", int64(999)).Return(&blockMeta)
	suite.blockStore.On("LoadBlockPart", int64(999), 0).Return(partSet.GetPart(0))

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

	suite.gossiper.GossipBlockPartsForCatchup(ctx, cstypes.RoundState{}, suite.ps.GetRoundState())

	// Same height, now with the real header and one part outstanding.
	suite.ps.PRS.ProposalBlockPartSetHeader = partSet.Header()
	suite.ps.PRS.ProposalBlockParts = partSet.BitArray().Not()

	for range 50 {
		suite.gossiper.GossipBlockPartsForCatchup(ctx, cstypes.RoundState{}, suite.ps.GetRoundState())
	}
	suite.Require().Equal(0, sends,
		"a budget opened against an unusable header must not fund sends after the peer swaps it out")

	// The next interval opens a fresh pass, budgeted on what the peer now reports.
	suite.clock.Advance(catchupResendInterval)
	for range 50 {
		suite.gossiper.GossipBlockPartsForCatchup(ctx, cstypes.RoundState{}, suite.ps.GetRoundState())
	}
	suite.Require().Equal(1, sends, "the fresh pass is worth the single part the peer now reports missing")
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
	// constructed with no expectations, so any call fails the test too.
	for range 10 {
		suite.gossiper.GossipBlockPartsForCatchup(ctx, cstypes.RoundState{}, suite.ps.GetRoundState())
	}
	suite.dataCh.AssertNotCalled(suite.T(), "Send", mock.Anything, mock.Anything)
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
