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

// A peer still in the new round step of this node's round after round 0 waits
// for transactions there, and is sent a prevote of that round at every pass:
// one copy can be lost after the channel took it, and nothing else would end
// the peer's wait.
func TestWaitingPeerPrevoteGossipHandler(t *testing.T) {
	ctx := context.Background()
	const chainID = "waiting-peer-test-chain"
	// privVals[i] signs for valSet.Validators[i].
	valSet, privVals := types.RandValidatorSet(4)
	peer := valSet.Validators[0].ProTxHash
	prevote := func(round int32) *types.Vote {
		stub := newValidatorStub(privVals[1], 1, 5)
		stub.Round = round
		return signVote(ctx, t, stub, tmproto.PrevoteType, chainID, types.BlockID{}, valSet.QuorumType, valSet.QuorumHash)
	}
	vote := prevote(2)

	stateData := func(round int32, version tmproto.VersionParams_ConsensusVersion, prevoted bool) StateData {
		votes := cstypes.NewHeightVoteSet(chainID, 5, valSet)
		votes.SetRound(round)
		if prevoted {
			v := vote
			if round != vote.Round {
				v = prevote(round)
			}
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
		wantSent  bool
	}{
		"should send a prevote to a validator waiting in this node's round": {
			stateData: stateData(2, v2, true), peer: waiting, peerID: peer, wantSent: true,
		},
		"should not send one to a peer in the propose step": {
			stateData: stateData(2, v2, true),
			peer:      cstypes.PeerRoundState{Height: 5, Round: 2, Step: cstypes.RoundStepPropose},
			peerID:    peer,
		},
		"should not send one to a peer in another round": {
			stateData: stateData(2, v2, true),
			peer:      cstypes.PeerRoundState{Height: 5, Round: 1, Step: cstypes.RoundStepNewRound},
			peerID:    peer,
		},
		"should not send one to a peer at another height": {
			stateData: stateData(2, v2, true),
			peer:      cstypes.PeerRoundState{Height: 4, Round: 2, Step: cstypes.RoundStepNewRound},
			peerID:    peer,
		},
		"should not send one without a prevote in the round": {
			stateData: stateData(2, v2, false), peer: waiting, peerID: peer,
		},
		"should not send one to a peer that is no validator": {
			stateData: stateData(2, v2, true), peer: waiting, peerID: crypto.RandProTxHash(),
		},
		"should not send one in round 0, which a single prevote does not end": {
			stateData: stateData(0, v2, true),
			peer:      cstypes.PeerRoundState{Height: 5, Round: 0, Step: cstypes.RoundStepNewRound},
			peerID:    peer,
		},
		"should not send one before version 2": {
			stateData: stateData(2, tmproto.VersionParams_CONSENSUS_VERSION_1, true), peer: waiting, peerID: peer,
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
			ps.SetProTxHash(tc.peerID)
			handler := waitingPeerPrevoteGossipHandler(ps, log.NewNopLogger(), send)
			handler(ctx, tc.stateData)
			if !tc.wantSent {
				require.Empty(t, sent)
				return
			}
			// Every pass sends it again: the peer is not taken to hold it.
			handler(ctx, tc.stateData)
			require.Len(t, sent, 2)
			for _, msg := range sent {
				require.Equal(t, vote.ToProto(), msg)
			}
		})
	}
}
