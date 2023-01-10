package consensus

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/internal/consensus/mocks"
	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/types"
)

type GossipHandlerTestSuite struct {
	suite.Suite

	gossiper *mocks.Gossiper
}

func TestGossipHandlerTestSuite(t *testing.T) {
	suite.Run(t, new(GossipHandlerTestSuite))
}

func (suite *GossipHandlerTestSuite) SetupTest() {
	suite.gossiper = &mocks.Gossiper{}
}

func (suite *GossipHandlerTestSuite) TearDownTest() {
	mock.AssertExpectationsForObjects(suite.T(), suite.gossiper)
}

func (suite *GossipHandlerTestSuite) TestQueryMaj23GossipHandler() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	valSet, _ := types.RandValidatorSet(1)
	appState := AppState{
		RoundState: cstypes.RoundState{},
		state: sm.State{
			Validators: valSet,
		},
	}
	testCases := []struct {
		peerState *PeerState
		appState  AppState
		mockFn    func(rs cstypes.RoundState, prs *cstypes.PeerRoundState)
	}{
		{
			peerState: &PeerState{
				ProTxHash: crypto.RandProTxHash(),
			},
			appState: appState,
		},
		{
			peerState: &PeerState{
				ProTxHash: valSet.GetProTxHashes()[0],
			},
			appState: appState,
			mockFn: func(rs cstypes.RoundState, prs *cstypes.PeerRoundState) {
				suite.gossiper.On("GossipVoteSetMaj23", ctx, rs, prs).Once()
			},
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("test-case #%d", i), func() {
			hd := queryMaj23GossipHandler(tc.peerState, suite.gossiper)
			if tc.mockFn != nil {
				tc.mockFn(tc.appState.RoundState, tc.peerState.GetRoundState())
			}
			hd(ctx, tc.appState)
		})
	}
}
