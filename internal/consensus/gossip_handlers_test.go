package consensus

import (
	"context"
	"fmt"
	"testing"

	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/consensus/mocks"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sm "github.com/dashpay/tenderdash/internal/state"
	statemocks "github.com/dashpay/tenderdash/internal/state/mocks"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/log"
	tmrand "github.com/dashpay/tenderdash/libs/rand"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

type GossipHandlerTestSuite struct {
	suite.Suite

	fakeGossiper   *mocks.Gossiper
	fakeBlockStore *statemocks.BlockStore
}

func TestGossipHandlerTestSuite(t *testing.T) {
	suite.Run(t, new(GossipHandlerTestSuite))
}

func (suite *GossipHandlerTestSuite) SetupTest() {
	suite.fakeGossiper = mocks.NewGossiper(suite.T())
	suite.fakeBlockStore = statemocks.NewBlockStore(suite.T())
}

func (suite *GossipHandlerTestSuite) TestQueryMaj23GossipHandler() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	valSet, _ := types.RandValidatorSet(1)
	stateData := StateData{
		RoundState: cstypes.RoundState{},
		state: sm.State{
			Validators: valSet,
		},
	}
	testCases := []struct {
		peerState *PeerState
		stateData StateData
		mockFn    func(rs cstypes.RoundState, prs *cstypes.PeerRoundState)
	}{
		{
			peerState: &PeerState{
				ProTxHash: crypto.RandProTxHash(),
			},
			stateData: stateData,
		},
		{
			peerState: &PeerState{
				ProTxHash: valSet.GetProTxHashes()[0],
			},
			stateData: stateData,
			mockFn: func(rs cstypes.RoundState, prs *cstypes.PeerRoundState) {
				suite.fakeGossiper.On("GossipVoteSetMaj23", ctx, rs, prs).Once()
			},
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("test-case #%d", i), func() {
			hd := queryMaj23GossipHandler(tc.peerState, suite.fakeGossiper)
			if tc.mockFn != nil {
				tc.mockFn(tc.stateData.RoundState, tc.peerState.GetRoundState())
			}
			hd(ctx, tc.stateData)
		})
	}
}

func (suite *GossipHandlerTestSuite) TestVotesAndCommitGossipHandler() {
	ctx := context.Background()
	valSet, _ := types.RandValidatorSet(1)
	committedState := sm.State{Validators: valSet}
	testCases := []struct {
		peerState *PeerState
		stateData StateData
		mockFn    func()
	}{
		{
			// should commit be gossiped
			peerState: &PeerState{
				PRS: cstypes.PeerRoundState{Height: 999, HasCommit: false},
			},
			stateData: StateData{
				RoundState: cstypes.RoundState{Height: 1000},
				state:      committedState,
			},
			mockFn: func() {
				suite.fakeGossiper.On("GossipCommit", ctx, mock.Anything, mock.Anything).Once()
			},
		},
		{
			// should vote be gossiped
			peerState: &PeerState{
				PRS:       cstypes.PeerRoundState{Height: 1000},
				ProTxHash: valSet.GetProTxHashes()[0],
			},
			stateData: StateData{
				RoundState: cstypes.RoundState{Height: 1000},
				state:      committedState,
			},
			mockFn: func() {
				suite.fakeGossiper.On("GossipVote", ctx, mock.Anything, mock.Anything).Once()
			},
		},
		{
			// should commit be gossiped for catchup
			peerState: &PeerState{
				PRS:       cstypes.PeerRoundState{Height: 998, HasCommit: false},
				ProTxHash: valSet.GetProTxHashes()[0],
			},
			stateData: StateData{
				RoundState: cstypes.RoundState{Height: 1000},
				state:      committedState,
			},
			mockFn: func() {
				suite.fakeBlockStore.On("Base").Once().Return(int64(1))
				suite.fakeGossiper.On("GossipCommit", ctx, mock.Anything, mock.Anything).Once()
			},
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("%d", i), func() {
			hd := votesAndCommitGossipHandler(tc.peerState, suite.fakeBlockStore, suite.fakeGossiper)
			if tc.mockFn != nil {
				tc.mockFn()
			}
			// the expectations are defined on the mock "gossiper", see mockFn function
			hd(ctx, tc.stateData)
		})
	}
}

func (suite *GossipHandlerTestSuite) TestDataGossipHandler() {
	const (
		partSetSize = 65536
		nParts      = 1
	)
	ctx := context.Background()
	logger := log.NewTestingLogger(suite.T())
	valSet, _ := factory.MockValidatorSet()
	committedState := sm.State{Validators: valSet}
	data := tmrand.Bytes(partSetSize * nParts)
	partSet1 := types.NewPartSetFromData(data, partSetSize)
	partSet1Empty := types.NewPartSetFromHeader(partSet1.Header())
	testCases := []struct {
		peerState *PeerState
		stateData StateData
		mockFn    func()
	}{
		{
			peerState: &PeerState{
				ProTxHash: valSet.GetProTxHashes()[0],
				PRS: cstypes.PeerRoundState{
					ProposalBlockParts:         partSet1Empty.BitArray(),
					ProposalBlockPartSetHeader: partSet1Empty.Header(),
				},
			},
			stateData: StateData{
				RoundState: cstypes.RoundState{
					ProposalBlockParts: partSet1,
				},
				state: committedState,
			},
			mockFn: func() {
				suite.fakeGossiper.On("GossipProposalBlockParts", ctx, mock.Anything, mock.Anything).Once()
			},
		},
		{
			peerState: &PeerState{
				ProTxHash: crypto.RandProTxHash(),
				PRS:       cstypes.PeerRoundState{HasCommit: true},
			},
			stateData: StateData{
				RoundState: cstypes.RoundState{
					ProposalBlockParts: partSet1,
				},
				state: committedState,
			},
			mockFn: func() {
				prsMatchedBy := mock.MatchedBy(func(prs *cstypes.PeerRoundState) bool {
					return prs.ProposalBlockParts.IsEmpty() && prs.ProposalBlockPartSetHeader.Total == 1
				})
				suite.fakeGossiper.On("GossipProposalBlockParts", ctx, mock.Anything, prsMatchedBy).Once()
			},
		},
		{
			peerState: &PeerState{
				ProTxHash: crypto.RandProTxHash(),
				PRS: cstypes.PeerRoundState{
					Height:                     999,
					ProposalBlockParts:         partSet1.BitArray(),
					ProposalBlockPartSetHeader: partSet1.Header(),
				},
			},
			stateData: StateData{
				RoundState: cstypes.RoundState{Height: 1000, ProposalBlockParts: partSet1},
				state:      committedState,
			},
			mockFn: func() {
				suite.fakeBlockStore.On("Base").Return(int64(1))
				suite.fakeGossiper.On("GossipBlockPartsForCatchup", ctx, mock.Anything, mock.Anything)
				suite.fakeGossiper.On("GossipCommit", ctx, mock.Anything, mock.Anything)
			},
		},
		{
			peerState: &PeerState{
				ProTxHash: valSet.GetProTxHashes()[0],
				PRS:       cstypes.PeerRoundState{Height: 1000, Proposal: false},
			},
			stateData: StateData{
				RoundState: cstypes.RoundState{
					Height:   1000,
					Proposal: &types.Proposal{},
				},
				state: committedState,
			},
			mockFn: func() {
				suite.fakeBlockStore.On("Base").Return(int64(1))
				suite.fakeGossiper.On("GossipProposal", ctx, mock.Anything, mock.Anything).Once()
			},
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("%d", i), func() {
			hd := dataGossipHandler(tc.peerState, logger, suite.fakeBlockStore, suite.fakeGossiper)
			if tc.mockFn != nil {
				tc.mockFn()
			}
			hd(ctx, tc.stateData)
		})
	}
}

func TestShouldProposalBeGossiped(t *testing.T) {
	testCases := []struct {
		rs          cstypes.RoundState
		prs         *cstypes.PeerRoundState
		isValidator bool
		want        bool
	}{
		{
			rs:          cstypes.RoundState{Proposal: nil},
			prs:         &cstypes.PeerRoundState{Proposal: false},
			isValidator: false,
			want:        false,
		},
		{
			rs:          cstypes.RoundState{Proposal: &types.Proposal{}},
			prs:         &cstypes.PeerRoundState{Proposal: false},
			isValidator: false,
			want:        false,
		},
		{
			rs:          cstypes.RoundState{Proposal: &types.Proposal{}},
			prs:         &cstypes.PeerRoundState{Proposal: true},
			isValidator: false,
			want:        false,
		},
		{
			rs:          cstypes.RoundState{Proposal: &types.Proposal{}},
			prs:         &cstypes.PeerRoundState{Proposal: true},
			isValidator: true,
			want:        false,
		},
		{
			rs:          cstypes.RoundState{Proposal: &types.Proposal{}},
			prs:         &cstypes.PeerRoundState{Proposal: false},
			isValidator: true,
			want:        true,
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			require.Equal(t, tc.want, shouldProposalBeGossiped(tc.rs, tc.prs, tc.isValidator))
		})
	}
}

func TestShouldBlockPartsBeGossiped(t *testing.T) {
	data := tmrand.Bytes(100)
	partSet := types.NewPartSetFromData(data, 100)
	testCases := []struct {
		rs          cstypes.RoundState
		prs         *cstypes.PeerRoundState
		isValidator bool
		want        bool
	}{
		{
			rs:          cstypes.RoundState{ProposalBlockParts: nil},
			prs:         &cstypes.PeerRoundState{},
			isValidator: false,
			want:        false,
		},
		{
			rs:          cstypes.RoundState{ProposalBlockParts: partSet},
			prs:         &cstypes.PeerRoundState{},
			isValidator: false,
			want:        false,
		},
		{
			rs:          cstypes.RoundState{ProposalBlockParts: partSet},
			prs:         &cstypes.PeerRoundState{ProposalBlockPartSetHeader: partSet.Header()},
			isValidator: false,
			want:        false,
		},
		{
			rs:          cstypes.RoundState{ProposalBlockParts: partSet},
			prs:         &cstypes.PeerRoundState{ProposalBlockPartSetHeader: partSet.Header()},
			isValidator: true,
			want:        true,
		},
		{
			rs:          cstypes.RoundState{ProposalBlockParts: partSet},
			prs:         &cstypes.PeerRoundState{HasCommit: true},
			isValidator: false,
			want:        true,
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			require.Equal(t, tc.want, shouldBlockPartsBeGossiped(tc.rs, tc.prs, tc.isValidator))
		})
	}
}

func TestShouldPeerBeCaughtUp(t *testing.T) {
	testCases := []struct {
		rs             cstypes.RoundState
		prs            *cstypes.PeerRoundState
		blockStoreBase int64
		want           bool
	}{
		{
			rs:             cstypes.RoundState{Height: 100},
			prs:            &cstypes.PeerRoundState{Height: 99},
			blockStoreBase: 0,
			want:           false,
		},
		{
			rs:             cstypes.RoundState{Height: 100},
			prs:            &cstypes.PeerRoundState{Height: 100},
			blockStoreBase: 1,
			want:           false,
		},
		{
			rs:             cstypes.RoundState{Height: 100},
			prs:            &cstypes.PeerRoundState{Height: 99},
			blockStoreBase: 1,
			want:           true,
		},
		{
			rs:             cstypes.RoundState{Height: 100},
			prs:            &cstypes.PeerRoundState{Height: 99},
			blockStoreBase: 99,
			want:           true,
		},
		{
			rs:             cstypes.RoundState{Height: 100},
			prs:            &cstypes.PeerRoundState{Height: 99},
			blockStoreBase: 100,
			want:           false,
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			require.Equal(t, tc.want, shouldPeerBeCaughtUp(tc.rs, tc.prs, tc.blockStoreBase))
		})
	}
}

func TestShouldCommitBeGossipedForCatchup(t *testing.T) {
	testCases := []struct {
		rs             cstypes.RoundState
		prs            *cstypes.PeerRoundState
		blockStoreBase int64
		want           bool
	}{
		{
			rs:             cstypes.RoundState{Height: 1000},
			prs:            &cstypes.PeerRoundState{Height: 1000},
			blockStoreBase: 1,
			want:           false,
		},
		{
			rs:             cstypes.RoundState{Height: 1000},
			prs:            &cstypes.PeerRoundState{Height: 999},
			blockStoreBase: 1,
			want:           false,
		},
		{
			rs:             cstypes.RoundState{Height: 1000},
			prs:            &cstypes.PeerRoundState{Height: 998},
			blockStoreBase: 1,
			want:           true,
		},
		{
			rs:             cstypes.RoundState{Height: 1000},
			prs:            &cstypes.PeerRoundState{Height: 998},
			blockStoreBase: 998,
			want:           true,
		},
		{
			rs:             cstypes.RoundState{Height: 1000},
			prs:            &cstypes.PeerRoundState{Height: 998},
			blockStoreBase: 999,
			want:           false,
		},
		{
			rs:             cstypes.RoundState{Height: 1000},
			prs:            &cstypes.PeerRoundState{Height: 998, HasCommit: true},
			blockStoreBase: 1,
			want:           false,
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			require.Equal(t, tc.want, shouldCommitBeGossipedForCatchup(tc.rs, tc.prs, tc.blockStoreBase))
		})
	}
}

func TestShouldCommitBeGossiped(t *testing.T) {
	testCases := []struct {
		rs   cstypes.RoundState
		prs  *cstypes.PeerRoundState
		want bool
	}{
		{
			rs:   cstypes.RoundState{Height: 1000},
			prs:  &cstypes.PeerRoundState{Height: 1000},
			want: false,
		},
		{
			rs:   cstypes.RoundState{Height: 1000},
			prs:  &cstypes.PeerRoundState{Height: 999},
			want: true,
		},
		{
			rs:   cstypes.RoundState{Height: 1},
			prs:  &cstypes.PeerRoundState{Height: 0},
			want: false,
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			require.Equal(t, tc.want, shouldCommitBeGossiped(tc.rs, tc.prs))
		})
	}
}

func TestShouldVoteBeGossiped(t *testing.T) {
	testCases := []struct {
		rs          cstypes.RoundState
		prs         *cstypes.PeerRoundState
		isValidator bool
		want        bool
	}{
		{
			rs:          cstypes.RoundState{Height: 1000},
			prs:         &cstypes.PeerRoundState{Height: 1000},
			isValidator: false,
			want:        false,
		},
		{
			rs:          cstypes.RoundState{Height: 1000},
			prs:         &cstypes.PeerRoundState{Height: 999},
			isValidator: true,
			want:        false,
		},
		{
			rs:          cstypes.RoundState{Height: 1000},
			prs:         &cstypes.PeerRoundState{Height: 1000},
			isValidator: true,
			want:        true,
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			require.Equal(t, tc.want, shouldVoteBeGossiped(tc.rs, tc.prs, tc.isValidator))
		})
	}
}

func TestRoundSkipGossipHandler(t *testing.T) {
	ctx := context.Background()
	certificate := &types.RoundSkipCertificate{
		Height:             5,
		Round:              2,
		QuorumHash:         crypto.RandQuorumHash(),
		ThresholdSignature: tmrand.Bytes(types.SignatureSize),
	}
	holding := StateData{RoundState: cstypes.RoundState{Height: 5, Round: 3, RoundSkip: certificate}}

	type held struct {
		height int64
		round  int32
	}
	testCases := map[string]struct {
		stateData StateData
		peer      cstypes.PeerRoundState
		// peerHolds is the certificate the peer sent this node, if any.
		peerHolds *held
		wantSent  bool
	}{
		"should send it to a peer still in the round it skips": {
			stateData: holding,
			peer:      cstypes.PeerRoundState{Height: 5, Round: 2, Step: cstypes.RoundStepNewRound},
			wantSent:  true,
		},
		"should send it to a peer in an earlier round": {
			stateData: holding,
			peer:      cstypes.PeerRoundState{Height: 5, Round: 0, Step: cstypes.RoundStepPropose},
			wantSent:  true,
		},
		"should send it to a peer in a later round, which may have followed prevotes there": {
			stateData: holding,
			peer:      cstypes.PeerRoundState{Height: 5, Round: 3, Step: cstypes.RoundStepPrevote},
			wantSent:  true,
		},
		"should not send it to a peer that sent it": {
			stateData: holding,
			peer:      cstypes.PeerRoundState{Height: 5, Round: 3, Step: cstypes.RoundStepNewRound},
			peerHolds: &held{height: 5, round: 2},
		},
		"should not send it to a peer that sent one for a later round": {
			stateData: holding,
			peer:      cstypes.PeerRoundState{Height: 5, Round: 4, Step: cstypes.RoundStepNewRound},
			peerHolds: &held{height: 5, round: 3},
		},
		"should send it to a peer that sent one for an earlier round": {
			stateData: holding,
			peer:      cstypes.PeerRoundState{Height: 5, Round: 2, Step: cstypes.RoundStepNewRound},
			peerHolds: &held{height: 5, round: 1},
			wantSent:  true,
		},
		"should send it to a peer that sent one at an earlier height": {
			stateData: holding,
			peer:      cstypes.PeerRoundState{Height: 5, Round: 2, Step: cstypes.RoundStepNewRound},
			peerHolds: &held{height: 4, round: 6},
			wantSent:  true,
		},
		"should not send it to a peer committing the height": {
			stateData: holding,
			peer:      cstypes.PeerRoundState{Height: 5, Round: 2, Step: cstypes.RoundStepApplyCommit},
		},
		"should not send it to a peer at another height": {
			stateData: holding,
			peer:      cstypes.PeerRoundState{Height: 4, Round: 2, Step: cstypes.RoundStepNewRound},
		},
		"should send nothing without a certificate": {
			stateData: StateData{RoundState: cstypes.RoundState{Height: 5, Round: 2}},
			peer:      cstypes.PeerRoundState{Height: 5, Round: 1, Step: cstypes.RoundStepNewRound},
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			var sent []proto.Message
			send := func(_ context.Context, msg proto.Message) error {
				sent = append(sent, msg)
				return nil
			}
			ps := &PeerState{PRS: tc.peer}
			if tc.peerHolds != nil {
				ps.SetHasRoundSkip(tc.peerHolds.height, tc.peerHolds.round)
			}
			roundSkipGossipHandler(ps, log.NewNopLogger(), send)(ctx, tc.stateData)
			if !tc.wantSent {
				require.Empty(t, sent)
				return
			}
			require.Len(t, sent, 1)
			msg, ok := sent[0].(*tmcons.RoundSkipCertificate)
			require.True(t, ok, "sent %T", sent[0])
			require.Equal(t, *certificate.ToProto(), msg.Certificate)
		})
	}
}

// A peer waiting in this node's round after round 0, or in a round this node
// left, is sent a vote of its round at every pass: one copy can be lost after
// the channel took it, and the peer may have nothing else to move it on. A
// peer in a round this node left is sent too the votes of a later round that
// +2/3 of any kind voted in, which this node may have reached on those alone.
func TestRoundVoteResendGossipHandler(t *testing.T) {
	ctx := context.Background()
	const chainID = "round-vote-resend-test-chain"
	// privVals[i] signs for valSet.Validators[i].
	valSet, privVals := types.RandValidatorSet(4)
	peer := valSet.Validators[0].ProTxHash
	// vote is validator val's vote of round, for no block.
	vote := func(val int32, voteType tmproto.SignedMsgType, round int32) *types.Vote {
		stub := newValidatorStub(privVals[val], val, 5)
		stub.Round = round
		return signVote(ctx, t, stub, voteType, chainID, types.BlockID{}, valSet.QuorumType, valSet.QuorumHash)
	}
	prevote0, precommit0 := vote(1, tmproto.PrevoteType, 0), vote(1, tmproto.PrecommitType, 0)
	prevote1, precommit1 := vote(1, tmproto.PrevoteType, 1), vote(1, tmproto.PrecommitType, 1)
	prevote2 := vote(1, tmproto.PrevoteType, 2)
	// Three of the four validators prevote in round 2: +2/3 of any kind.
	prevotes2 := []*types.Vote{prevote2, vote(2, tmproto.PrevoteType, 2), vote(3, tmproto.PrevoteType, 2)}
	prevote3 := vote(1, tmproto.PrevoteType, 3)

	stateData := func(round int32, version tmproto.VersionParams_ConsensusVersion, held ...*types.Vote) StateData {
		votes := cstypes.NewHeightVoteSet(chainID, 5, valSet)
		votes.SetRound(round)
		for _, v := range held {
			added, err := votes.AddVote(v)
			require.NoError(t, err)
			require.True(t, added)
		}
		params := *types.DefaultConsensusParams()
		params.Version.ConsensusVersion = int32(version)
		return StateData{
			state:      sm.State{Validators: valSet, ConsensusParams: params},
			RoundState: cstypes.RoundState{Height: 5, Round: round, Step: cstypes.RoundStepPrevote, Validators: valSet, Votes: votes},
		}
	}
	waiting := cstypes.PeerRoundState{Height: 5, Round: 2, Step: cstypes.RoundStepNewRound}
	v2 := tmproto.VersionParams_CONSENSUS_VERSION_2

	testCases := map[string]struct {
		stateData StateData
		peer      cstypes.PeerRoundState
		peerID    types.ProTxHash
		// from are the votes the passes may send, none if it is empty.
		from []*types.Vote
		// sends are votes of from the passes send each of.
		sends []*types.Vote
	}{
		"should send a prevote to a validator waiting in this node's round": {
			stateData: stateData(2, v2, prevote1, precommit1, prevote2), peer: waiting, peerID: peer,
			from: []*types.Vote{prevote2}, sends: []*types.Vote{prevote2},
		},
		"should not send one to a peer in the propose step of this node's round": {
			stateData: stateData(2, v2, prevote2),
			peer:      cstypes.PeerRoundState{Height: 5, Round: 2, Step: cstypes.RoundStepPropose},
			peerID:    peer,
		},
		"should send the prevotes and precommits of its round to a peer waiting in a round this node left": {
			stateData: stateData(2, v2, prevote1, precommit1, prevote2),
			peer:      cstypes.PeerRoundState{Height: 5, Round: 1, Step: cstypes.RoundStepNewRound},
			peerID:    peer,
			from:      []*types.Vote{prevote1, precommit1},
			sends:     []*types.Vote{prevote1, precommit1},
		},
		"should send them to a peer in any step of round 0 once this node left it": {
			stateData: stateData(2, v2, prevote0, precommit0, prevote2),
			peer:      cstypes.PeerRoundState{Height: 5, Round: 0, Step: cstypes.RoundStepPrevote},
			peerID:    peer,
			from:      []*types.Vote{prevote0, precommit0},
			sends:     []*types.Vote{prevote0, precommit0},
		},
		"should send the votes of the latest later round with +2/3 of any kind to a peer in a round this node holds no vote of": {
			stateData: stateData(3, v2, append(append([]*types.Vote{}, prevotes2...), prevote3)...),
			peer:      cstypes.PeerRoundState{Height: 5, Round: 1, Step: cstypes.RoundStepNewRound},
			peerID:    peer,
			from:      prevotes2,
		},
		"should send both its round's votes and a later round's by turns": {
			stateData: stateData(3, v2, append([]*types.Vote{prevote1}, prevotes2...)...),
			peer:      cstypes.PeerRoundState{Height: 5, Round: 1, Step: cstypes.RoundStepPrevote},
			peerID:    peer,
			from:      append([]*types.Vote{prevote1}, prevotes2...),
			sends:     []*types.Vote{prevote1},
		},
		"should not send one to a peer in a later round": {
			stateData: stateData(2, v2, prevote2),
			peer:      cstypes.PeerRoundState{Height: 5, Round: 3, Step: cstypes.RoundStepNewRound},
			peerID:    peer,
		},
		"should not send one to a peer at another height": {
			stateData: stateData(2, v2, prevote2),
			peer:      cstypes.PeerRoundState{Height: 4, Round: 2, Step: cstypes.RoundStepNewRound},
			peerID:    peer,
		},
		"should not send one without a vote in the peer's round or +2/3 in a later one": {
			stateData: stateData(3, v2, prevote3),
			peer:      cstypes.PeerRoundState{Height: 5, Round: 1, Step: cstypes.RoundStepNewRound},
			peerID:    peer,
		},
		"should not send one to a peer waiting in this node's round without a prevote in it": {
			stateData: stateData(2, v2, prevote1), peer: waiting, peerID: peer,
		},
		"should not send one to a peer that is no validator": {
			stateData: stateData(2, v2, prevote2), peer: waiting, peerID: crypto.RandProTxHash(),
		},
		"should not send one to a peer waiting in round 0, which a single prevote does not end": {
			stateData: stateData(0, v2, prevote0),
			peer:      cstypes.PeerRoundState{Height: 5, Round: 0, Step: cstypes.RoundStepNewRound},
			peerID:    peer,
		},
		"should not send one before version 2": {
			stateData: stateData(2, tmproto.VersionParams_CONSENSUS_VERSION_1, prevote1, precommit1, prevote2),
			peer:      cstypes.PeerRoundState{Height: 5, Round: 1, Step: cstypes.RoundStepNewRound},
			peerID:    peer,
		},
	}
	const passes = 4
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			var sent []proto.Message
			send := func(_ context.Context, msg proto.Message) error {
				sent = append(sent, msg)
				return nil
			}
			ps := &PeerState{PRS: tc.peer}
			ps.SetProTxHash(tc.peerID)
			handler := roundVoteResendGossipHandler(ps, log.NewNopLogger(), send)
			// Every pass sends one: the peer is not taken to hold it.
			for range passes {
				handler(ctx, tc.stateData)
			}
			if len(tc.from) == 0 {
				require.Empty(t, sent)
				return
			}
			require.Len(t, sent, passes)
			from := make([]proto.Message, 0, len(tc.from))
			for _, v := range tc.from {
				from = append(from, v.ToProto())
			}
			for _, msg := range sent {
				require.Contains(t, from, msg)
			}
			for _, v := range tc.sends {
				require.Contains(t, sent, v.ToProto())
			}
		})
	}
}

// A validator peer at this height, in an earlier round, collecting the block
// this node holds as valid or locked is served its parts from there, since the
// round that left it dropped the proposal part set. The part set header names
// the block; nothing is served on round or hash alone, to another height, or to
// a non-validator.
func (suite *GossipHandlerTestSuite) TestDataGossipHandlerServesHeldBlockToLaggingPeer() {
	ctx := context.Background()
	logger := log.NewTestingLogger(suite.T())
	valSet, _ := factory.MockValidatorSet()
	validator, nonValidator := valSet.GetProTxHashes()[0], crypto.RandProTxHash()
	held := types.NewPartSetFromData(tmrand.Bytes(200), 100)
	other := types.NewPartSetFromData(tmrand.Bytes(100), 100)
	incomplete := types.NewPartSetFromHeader(held.Header())
	_, err := incomplete.AddPart(held.GetPart(0))
	suite.Require().NoError(err)
	collecting := func(parts *types.PartSet, height int64, round int32) cstypes.PeerRoundState {
		return cstypes.PeerRoundState{
			Height:                     height,
			Round:                      round,
			ProposalBlockPartSetHeader: parts.Header(),
			ProposalBlockParts:         types.NewPartSetFromHeader(parts.Header()).BitArray(),
		}
	}
	testCases := []struct {
		name      string
		proTxHash crypto.ProTxHash
		rs        cstypes.RoundState
		prs       cstypes.PeerRoundState
		served    bool
	}{
		{
			name:      "valid block to a peer in an earlier round",
			proTxHash: validator,
			rs:        cstypes.RoundState{Height: 10, Round: 2, ValidRound: 1, ValidBlockParts: held},
			prs:       collecting(held, 10, 1),
			served:    true,
		},
		{
			name:      "locked block to a peer in an earlier round",
			proTxHash: validator,
			rs:        cstypes.RoundState{Height: 10, Round: 2, LockedRound: 1, LockedBlockParts: held},
			prs:       collecting(held, 10, 1),
			served:    true,
		},
		{
			name:      "valid block while this round proposes another",
			proTxHash: validator,
			rs:        cstypes.RoundState{Height: 10, Round: 2, ValidRound: 1, ValidBlockParts: held, ProposalBlockParts: other},
			prs:       collecting(held, 10, 1),
			served:    true,
		},
		{
			name:      "not a block the peer collects",
			proTxHash: validator,
			rs:        cstypes.RoundState{Height: 10, Round: 2, ValidRound: 1, ValidBlockParts: held},
			prs:       collecting(other, 10, 1),
		},
		{
			name:      "not to another height",
			proTxHash: validator,
			rs:        cstypes.RoundState{Height: 10, Round: 2, ValidRound: 1, ValidBlockParts: held},
			prs:       collecting(held, 11, 1),
		},
		{
			name:      "not to a peer in this round or a later one",
			proTxHash: validator,
			rs:        cstypes.RoundState{Height: 10, Round: 2, ValidRound: 1, ValidBlockParts: held},
			prs:       collecting(held, 10, 2),
		},
		{
			name:      "not an incomplete block",
			proTxHash: validator,
			rs:        cstypes.RoundState{Height: 10, Round: 2, ValidRound: 1, ValidBlockParts: incomplete},
			prs:       collecting(held, 10, 1),
		},
		{
			name:      "not to a non-validator",
			proTxHash: nonValidator,
			rs:        cstypes.RoundState{Height: 10, Round: 2, ValidRound: 1, ValidBlockParts: held},
			prs:       collecting(held, 10, 1),
		},
	}
	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			suite.SetupTest()
			ps := &PeerState{ProTxHash: tc.proTxHash, PRS: tc.prs}
			stateData := StateData{RoundState: tc.rs, state: sm.State{Validators: valSet}}
			if tc.served {
				// The part set served and the round it is marked as the peer's
				// under are the peer's, on the handler's copy of the snapshot.
				rsMatchedBy := mock.MatchedBy(func(rs cstypes.RoundState) bool {
					return rs.ProposalBlockParts == held && rs.Round == tc.prs.Round
				})
				suite.fakeGossiper.On("GossipProposalBlockParts", ctx, rsMatchedBy, mock.Anything).Once()
			} else {
				suite.fakeBlockStore.On("Base").Return(int64(1)).Maybe()
			}
			hd := dataGossipHandler(ps, logger, suite.fakeBlockStore, suite.fakeGossiper)
			hd(ctx, stateData)
		})
	}
}
