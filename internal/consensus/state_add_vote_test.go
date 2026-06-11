package consensus

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/internal/eventbus"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmrequire "github.com/dashpay/tenderdash/internal/test/require"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
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
	valSet, privVals := factory.MockValidatorSet()
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
		prevote: func(_ctx context.Context, _stateData *StateData, _vote *types.Vote) (bool, error) {
			prevoteCalled = true
			return true, nil
		},
		precommit: func(_ctx context.Context, _stateData *StateData, _vote *types.Vote) (bool, error) {
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
	const H100 = int64(100)
	eventFired := false
	suite.emitter.AddListener(types.EventVoteValue, func(_data eventemitter.EventData) error {
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
			Votes: cstypes.NewHeightVoteSet(factory.DefaultTestChainID, H100, suite.valSet),
		},
	}
	val0 := suite.valSet.Validators[0]
	blockID := types.BlockID{
		Hash: tmbytes.MustHexDecode("1D03D1D81E94A099042736D40BD9681B867321443FF58A4568E274DBD83BFFEB"),
	}
	voteH100R0 := types.Vote{
		Type:               tmproto.PrevoteType,
		Height:             H100,
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
		suite.Run(fmt.Sprintf("%d", i), func() {
			stateData.Votes = cstypes.NewHeightVoteSet(factory.DefaultTestChainID, H100, suite.valSet)
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
	suite.emitter.AddListener(types.EventValidBlockValue, func(_data eventemitter.EventData) error {
		eventFired = true
		return nil
	})
	val0 := suite.valSet.Validators[0]
	val1 := suite.valSet.Validators[1]
	blockID := types.BlockID{
		Hash: tmbytes.MustHexDecode("1D03D1D81E94A099042736D40BD9681B867321443FF58A4568E274DBD83BFFEB"),
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
	mockFn := func(_ctx context.Context, _stateData *StateData, _vote *types.Vote) (bool, error) {
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
			hvs := cstypes.NewHeightVoteSet(factory.DefaultTestChainID, 100, suite.valSet)
			for _, vote := range tc.presetVotes {
				added, err := hvs.AddVote(&vote)
				suite.NoError(err)
				suite.True(added)
			}
			eventFired = false
			returnAdded = tc.returnAdded
			returnError = tc.returnError
			store := &StateDataStore{emitter: suite.emitter}
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
			tmrequire.Error(suite.T(), tc.wantErr, err)
			suite.Equal(tc.wantAdded, added)
			suite.Equal(tc.wantFiredEvent, eventFired)
			suite.Equal(tc.wantStateDataVer, store.version)
		})
	}
}

// TestAddVoteValidatorIndexBounds asserts that a vote carrying a validator
// index outside the active validator set is rejected with a validation error
// before any validator-set lookup, and that a valid index is passed through.
//
// The production middlewares are composed over a recording sentinel in the same
// order as newAddVoteAction, so the bounds guard in addVoteValidateVoteMw runs
// against the real wiring. The assembled chain is preferred over a full
// AddVoteAction here because the latter requires a live blockExec/Controller and
// signed extensions that are irrelevant to the bounds check.
func TestAddVoteValidatorIndexBounds(t *testing.T) {
	ctx := context.Background()
	emitter := eventemitter.New()
	valSet, privVals := factory.MockValidatorSet()
	setSize := int32(valSet.Size())
	const height = int64(100)

	blockID := types.BlockID{
		Hash: tmbytes.MustHexDecode("1D03D1D81E94A099042736D40BD9681B867321443FF58A4568E274DBD83BFFEB"),
	}
	// A non-local validator: the precommit verify-extension middleware only
	// short-circuits for the local validator, so an empty privVal keeps that
	// middleware on the path and exercises guard #2's wiring as well.
	remoteProTxHash := valSet.Validators[0].ProTxHash
	localProTxHash := valSet.Validators[1].ProTxHash

	newState := func() *StateData {
		return &StateData{
			state: sm.State{Validators: valSet},
			RoundState: cstypes.RoundState{
				Height:     height,
				Round:      0,
				Validators: valSet,
				Votes:      cstypes.NewHeightVoteSet(factory.DefaultTestChainID, height, valSet),
			},
		}
	}

	// reached records whether the inner AddVoteFunc was invoked, i.e. the vote
	// survived every guard. The sentinel never touches the validator set.
	build := func(voteType tmproto.SignedMsgType, privVal privValidator, reached *bool) AddVoteFunc {
		sentinel := func(_ context.Context, _ *StateData, _ *types.Vote) (bool, error) {
			*reached = true
			return true, nil
		}
		validateMw := addVoteValidateVoteMw()
		if voteType == tmproto.PrecommitType {
			verifyMw := addVoteVerifyVoteExtensionMw(privVal, nil, NopMetrics(), emitter)
			return withVoterMws(sentinel, verifyMw, validateMw)
		}
		return withVoterMws(sentinel, validateMw)
	}

	testCases := []struct {
		name        string
		voteType    tmproto.SignedMsgType
		index       int32
		proTxHash   types.ProTxHash
		localValIdx int // index in privVals used as the local validator (-1 => none)
		wantReached bool
		wantErr     bool
	}{
		{
			name:        "precommit index == setSize",
			voteType:    tmproto.PrecommitType,
			index:       setSize,
			proTxHash:   remoteProTxHash,
			localValIdx: 1,
			wantErr:     true,
		},
		{
			name:        "precommit index == setSize+1",
			voteType:    tmproto.PrecommitType,
			index:       setSize + 1,
			proTxHash:   remoteProTxHash,
			localValIdx: 1,
			wantErr:     true,
		},
		{
			name:        "precommit index == 2_000_000_000",
			voteType:    tmproto.PrecommitType,
			index:       2_000_000_000,
			proTxHash:   remoteProTxHash,
			localValIdx: 1,
			wantErr:     true,
		},
		{
			name:        "precommit negative index",
			voteType:    tmproto.PrecommitType,
			index:       -1,
			proTxHash:   remoteProTxHash,
			localValIdx: 1,
			wantErr:     true,
		},
		{
			name:        "prevote index == setSize",
			voteType:    tmproto.PrevoteType,
			index:       setSize,
			proTxHash:   remoteProTxHash,
			localValIdx: -1,
			wantErr:     true,
		},
		{
			// The vote's ProTxHash equals the local validator, so the verify
			// middleware would normally short-circuit; guard #1 must still
			// reject the out-of-range index before that happens.
			name:        "precommit out-of-range from local validator",
			voteType:    tmproto.PrecommitType,
			index:       setSize,
			proTxHash:   localProTxHash,
			localValIdx: 1,
			wantErr:     true,
		},
		{
			name:        "valid precommit index from local validator is processed",
			voteType:    tmproto.PrecommitType,
			index:       1,
			proTxHash:   localProTxHash,
			localValIdx: 1,
			wantReached: true,
		},
		{
			name:        "valid prevote index is processed",
			voteType:    tmproto.PrevoteType,
			index:       0,
			proTxHash:   remoteProTxHash,
			localValIdx: -1,
			wantReached: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var privVal privValidator
			if tc.localValIdx >= 0 {
				privVal = privValidator{
					PrivValidator: privVals[tc.localValIdx],
					ProTxHash:     valSet.Validators[tc.localValIdx].ProTxHash,
				}
			}
			var reached bool
			fn := build(tc.voteType, privVal, &reached)
			vote := &types.Vote{
				Type:               tc.voteType,
				Height:             height,
				Round:              0,
				BlockID:            blockID,
				ValidatorProTxHash: tc.proTxHash,
				ValidatorIndex:     tc.index,
			}
			var (
				added bool
				err   error
			)
			require.NotPanics(t, func() {
				added, err = fn(ctx, newState(), vote)
			})
			if tc.wantErr {
				require.Error(t, err)
				require.False(t, added)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.wantReached, reached)
		})
	}
}

// TestAddVoteVerifyExtensionMwIndexNotInStateValidators covers guard #2 in
// isolation: guard #1 bounds against stateData.Validators (RoundState), but the
// verify-extension middleware looks up stateData.state.Validators, which can be
// a different, smaller set across a validator-set rotation. With an index valid
// for RoundState but out of range for state.Validators, the nil-check must
// reject the vote before dereferencing the (nil) validator's public key.
func TestAddVoteVerifyExtensionMwIndexNotInStateValidators(t *testing.T) {
	ctx := context.Background()
	emitter := eventemitter.New()
	roundSet, privVals := factory.MockValidatorSet() // size 2

	// state.Validators holds only the first validator, so index 1 is valid for
	// the RoundState set (guard #1 passes) yet out of range here (guard #2 must
	// catch it). blockExec is nil: guard #2 returns before it is reached.
	smallerSet := roundSet.Copy()
	smallerSet.Validators = smallerSet.Validators[:1]

	stateData := &StateData{
		state: sm.State{
			ChainID:    factory.DefaultTestChainID,
			Validators: smallerSet,
		},
		RoundState: cstypes.RoundState{
			Height:     100,
			Round:      0,
			Validators: roundSet,
			Votes:      cstypes.NewHeightVoteSet(factory.DefaultTestChainID, 100, roundSet),
		},
	}

	// privVal must NOT equal the vote's validator, otherwise the middleware
	// short-circuits before reaching the lookup.
	privVal := privValidator{
		PrivValidator: privVals[0],
		ProTxHash:     roundSet.Validators[0].ProTxHash,
	}
	verifyMw := addVoteVerifyVoteExtensionMw(privVal, nil, NopMetrics(), emitter)

	reached := false
	fn := verifyMw(func(_ context.Context, _ *StateData, _ *types.Vote) (bool, error) {
		reached = true
		return true, nil
	})

	vote := &types.Vote{
		Type:               tmproto.PrecommitType,
		Height:             100,
		Round:              0,
		BlockID:            types.BlockID{Hash: tmbytes.MustHexDecode("1D03D1D81E94A099042736D40BD9681B867321443FF58A4568E274DBD83BFFEB")},
		ValidatorProTxHash: roundSet.Validators[1].ProTxHash,
		ValidatorIndex:     1,
	}

	var (
		added bool
		err   error
	)
	require.NotPanics(t, func() {
		added, err = fn(ctx, stateData, vote)
	})
	require.Error(t, err)
	require.False(t, added)
	require.False(t, reached)
}
