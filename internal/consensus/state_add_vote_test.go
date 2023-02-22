package consensus

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/tendermint/tendermint/types"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	"github.com/tendermint/tendermint/internal/eventbus"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/libs/eventemitter"
	"github.com/tendermint/tendermint/libs/log"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
)

type AddVoteTestSuite struct {
	suite.Suite

	logger    log.Logger
	metrics   *Metrics
	emitter   *eventemitter.EventEmitter
	eventbus  *eventbus.EventBus
	publisher *EventPublisher
	signer    testSigner
	valSet    *types.ValidatorSet
}

func TestAddVote(t *testing.T) {
	suite.Run(t, new(AddVoteTestSuite))
}

func (suite *AddVoteTestSuite) SetupTest() {
	ctx := context.Background()
	suite.logger = log.NewTestingLogger(suite.T())
	suite.metrics = NopMetrics()
	suite.emitter = eventemitter.New()
	suite.eventbus = eventbus.NewDefault(suite.logger)
	err := suite.eventbus.Start(ctx)
	suite.NoError(err)
	suite.publisher = &EventPublisher{eventBus: suite.eventbus, emitter: suite.emitter}
	valSet, privVals := mockValidatorSet()
	suite.signer = testSigner{privVals: privVals, valSet: valSet}
	suite.valSet = valSet
}

func (suite *AddVoteTestSuite) TearDownTest() {
	suite.eventbus.Stop()
}

func (suite *AddVoteTestSuite) TestAddVoteAction() {
	ctx := context.Background()
	prevoteCalled := false
	precommitCalled := false
	cmd := AddVoteAction{
		prevote: func(ctx context.Context, stateData *StateData, vote *types.Vote) (bool, error) {
			prevoteCalled = true
			return true, nil
		},
		precommit: func(ctx context.Context, stateData *StateData, vote *types.Vote) (bool, error) {
			precommitCalled = true
			return true, nil
		},
	}
	testCases := []struct {
		vote          *types.Vote
		wantPrevote   bool
		wantPrecommit bool
	}{
		{
			vote:        &types.Vote{Type: tmproto.PrevoteType},
			wantPrevote: true,
		},
		{
			vote:          &types.Vote{Type: tmproto.PrecommitType},
			wantPrecommit: true,
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("test-case #%d", i), func() {
			prevoteCalled = false
			precommitCalled = false
			stateEvent := StateEvent{
				Data: &AddVoteEvent{
					Vote: tc.vote,
				},
			}
			err := cmd.Execute(ctx, stateEvent)
			suite.NoError(err)
			suite.Equal(tc.wantPrevote, prevoteCalled)
			suite.Equal(tc.wantPrecommit, precommitCalled)
		})
	}
}

func (suite *AddVoteTestSuite) TestAddVoteToVoteSet() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eventFired := false
	suite.emitter.AddListener(types.EventVoteValue, func(data eventemitter.EventData) error {
		eventFired = true
		return nil
	})
	fn := addVoteToVoteSetFunc(suite.metrics, suite.publisher)
	stateData := &StateData{
		state: sm.State{
			Validators: suite.valSet,
		},
		RoundState: cstypes.RoundState{
			Round: 0,
			Votes: cstypes.NewHeightVoteSet(chainID, 100, suite.valSet),
		},
	}
	val0 := suite.valSet.Validators[0]
	blockID := types.BlockID{
		Hash: mustHexToBytes("1D03D1D81E94A099042736D40BD9681B867321443FF58A4568E274DBD83BFFEB"),
	}
	voteH100R0 := types.Vote{
		Type:               tmproto.PrevoteType,
		Height:             100,
		Round:              0,
		BlockID:            blockID,
		ValidatorProTxHash: val0.ProTxHash,
		ValidatorIndex:     0,
	}
	voteH100R1 := voteH100R0
	voteH100R1.Round = 1
	err := suite.signer.signVotes(ctx, &voteH100R0, &voteH100R1)
	require.NoError(suite.T(), err)
	testCases := []struct {
		vote           types.Vote
		wantAdded      bool
		wantErr        string
		wantFiredEvent bool
	}{
		{
			vote: types.Vote{},
		},
		{
			vote:           voteH100R0,
			wantAdded:      true,
			wantFiredEvent: true,
		},
		{
			vote:           voteH100R1,
			wantAdded:      true,
			wantFiredEvent: true,
		},
	}
	for i, tc := range testCases {
		eventFired = false
		suite.Run(fmt.Sprintf("test-case #%d", i), func() {
			stateData.Votes = cstypes.NewHeightVoteSet(chainID, 100, suite.valSet)
			added, err := fn(ctx, stateData, &tc.vote)
			suite.NoError(err)
			suite.Equal(tc.wantAdded, added)
			suite.Equal(tc.wantFiredEvent, eventFired)
		})
	}
}

func (suite *AddVoteTestSuite) TestAddVoteUpdateValidBlockMw() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eventFired := false
	suite.emitter.AddListener(types.EventValidBlockValue, func(data eventemitter.EventData) error {
		eventFired = true
		return nil
	})
	val0 := suite.valSet.Validators[0]
	val1 := suite.valSet.Validators[1]
	blockID := types.BlockID{
		Hash: mustHexToBytes("1D03D1D81E94A099042736D40BD9681B867321443FF58A4568E274DBD83BFFEB"),
	}
	voteH100R0V0 := types.Vote{
		Type:               tmproto.PrevoteType,
		Height:             100,
		Round:              0,
		BlockID:            blockID,
		ValidatorProTxHash: val0.ProTxHash,
		ValidatorIndex:     0,
	}
	voteH100R0V1 := voteH100R0V0
	voteH100R0V1.ValidatorProTxHash = val1.ProTxHash
	voteH100R0V1.ValidatorIndex = 1
	voteNilH100R0V0 := voteH100R0V0
	voteNilH100R0V0.BlockID = types.BlockID{}
	err := suite.signer.signVotes(ctx, &voteH100R0V0, &voteH100R0V1, &voteNilH100R0V0)
	suite.NoError(err)
	returnAdded := true
	var returnError error
	mockFn := func(ctx context.Context, stateData *StateData, vote *types.Vote) (bool, error) {
		return returnAdded, returnError
	}
	fn := addVoteUpdateValidBlockMw(suite.publisher)(mockFn)
	testCases := []struct {
		presetVotes      []types.Vote
		vote             types.Vote
		wantAdded        bool
		wantErr          string
		wantFiredEvent   bool
		returnAdded      bool
		returnError      error
		wantStateDataVer int64
	}{
		{
			presetVotes:      []types.Vote{voteH100R0V0, voteH100R0V1},
			vote:             voteH100R0V1,
			wantAdded:        true,
			wantFiredEvent:   true,
			returnAdded:      true,
			wantStateDataVer: 1,
		},
		{
			presetVotes:    []types.Vote{voteH100R0V0},
			vote:           voteH100R0V0,
			wantAdded:      true,
			wantFiredEvent: false,
			returnAdded:    true,
		},
		{
			vote:           voteH100R0V0,
			wantAdded:      true,
			wantFiredEvent: false,
			returnAdded:    true,
			returnError:    nil,
		},
		{
			presetVotes:    []types.Vote{voteNilH100R0V0},
			vote:           voteNilH100R0V0,
			wantAdded:      true,
			wantFiredEvent: false,
			returnAdded:    true,
			returnError:    nil,
		},
		{
			wantAdded:      false,
			wantFiredEvent: false,
			returnAdded:    false,
			returnError:    nil,
		},
		{
			wantAdded:      true,
			wantErr:        "error",
			wantFiredEvent: false,
			returnAdded:    true,
			returnError:    errors.New("error"),
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("test-case #%d", i), func() {
			hvs := cstypes.NewHeightVoteSet(chainID, 100, suite.valSet)
			for _, vote := range tc.presetVotes {
				added, err := hvs.AddVote(&vote)
				suite.NoError(err)
				suite.True(added)
			}
			eventFired = false
			returnAdded = tc.returnAdded
			returnError = tc.returnError
			store := &StateDataStore{evws: suite.evsw}
			stateData := &StateData{
				store: store,
				state: sm.State{
					Validators: suite.valSet,
				},
				RoundState: cstypes.RoundState{
					Round:      0,
					Votes:      hvs,
					ValidRound: -1,
				},
			}
			added, err := fn(ctx, stateData, &tc.vote)
			assertError(suite.T(), tc.wantErr, err)
			suite.Equal(tc.wantAdded, added)
			suite.Equal(tc.wantFiredEvent, eventFired)
			suite.Equal(tc.wantStateDataVer, store.version)
		})
	}
}
