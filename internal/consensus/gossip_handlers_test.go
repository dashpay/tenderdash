package consensus

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/internal/consensus/mocks"
	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	sm "github.com/tendermint/tendermint/internal/state"
	statemocks "github.com/tendermint/tendermint/internal/state/mocks"
	"github.com/tendermint/tendermint/libs/log"
	tmrand "github.com/tendermint/tendermint/libs/rand"
	"github.com/tendermint/tendermint/types"
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
	valSet, _ := mockValidatorSet()
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
				suite.fakeGossiper.On("GossipBlockPartsAndCommitForCatchup", ctx, mock.Anything, mock.Anything)
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
