package quorum

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	dbm "github.com/tendermint/tm-db"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/dash"
	"github.com/dashpay/tenderdash/dash/quorum/mock"
	"github.com/dashpay/tenderdash/dash/quorum/selectpeers"
	"github.com/dashpay/tenderdash/internal/eventbus"
	"github.com/dashpay/tenderdash/internal/mempool/mocks"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/proxy"
	"github.com/dashpay/tenderdash/internal/pubsub"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/store"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

const (
	mySeedID uint16 = math.MaxUint16 - 1
)

type validatorUpdate struct {
	validators      []*types.Validator
	expectedHistory []mock.HistoryEvent
}
type testCase struct {
	me               *types.Validator
	validatorUpdates []validatorUpdate
}

// TestValidatorConnExecutorNotValidator checks what happens if current node is not a validator.
// Expected: nothing happens
func TestValidatorConnExecutor_NotValidator(t *testing.T) {

	me := mock.NewValidator(mySeedID)
	tc := testCase{
		me: me,
		validatorUpdates: []validatorUpdate{
			0: {
				validators: []*types.Validator{
					mock.NewValidator(1),
					mock.NewValidator(2),
					mock.NewValidator(3),
				},
				expectedHistory: []mock.HistoryEvent{},
			}},
	}
	executeTestCase(t, tc)
}

// TestValidatorConnExecutor_WrongAddress checks behavior in case of several issues in the address.
// Expected behavior: invalid address is dialed. Previous addresses are disconnected.
func TestValidatorConnExecutor_WrongAddress(t *testing.T) {
	me := mock.NewValidator(mySeedID)
	nodeID := mock.NewNodeID(1000)
	addr1, err := types.ParseValidatorAddress("http://" + nodeID + "@www.domain-that-does-not-exist.com:80")
	require.NoError(t, err)

	val1 := mock.NewValidator(100)
	val1.NodeAddress = addr1

	valsWithoutAddress := make([]*types.Validator, 5)
	for i := 0; i < len(valsWithoutAddress); i++ {
		valsWithoutAddress[i] = mock.NewValidator(uint16(200 + i))
		valsWithoutAddress[i].NodeAddress = types.ValidatorAddress{}
	}

	tc := testCase{
		me: me,
		validatorUpdates: []validatorUpdate{
			0: {
				validators: []*types.Validator{
					me,
					mock.NewValidator(1),
					mock.NewValidator(2),
					mock.NewValidator(3),
					mock.NewValidator(4),
				},
				expectedHistory: []mock.HistoryEvent{
					{Operation: mock.OpDial},
					{Operation: mock.OpDial},
				},
			},
			1: { // val1 has invalid address, but we still pass it to dial, so it will be here
				validators: []*types.Validator{
					me,
					val1,
				},
				expectedHistory: []mock.HistoryEvent{
					{Operation: mock.OpStop},
					{Operation: mock.OpStop},
					{Operation: mock.OpDial, Params: []string{string(val1.NodeAddress.NodeID)}},
				},
			},
			2: { // disconnect val1and dial 2 new validators (skipping invalid one)
				validators: []*types.Validator{
					me,
					valsWithoutAddress[0],
					mock.NewValidator(1),
					mock.NewValidator(2),
					mock.NewValidator(3),
					mock.NewValidator(4),
					mock.NewValidator(5),
				},
				expectedHistory: []mock.HistoryEvent{
					{Operation: mock.OpStop, Params: []string{string(val1.NodeAddress.NodeID)}},
					{Operation: mock.OpDial, Params: []string{
						mock.NewNodeID(2),
						mock.NewNodeID(5),
					}},
					{Operation: mock.OpDial, Params: []string{
						mock.NewNodeID(2),
						mock.NewNodeID(5),
					}},
				},
			},
			3: { // this should disconnect everyone because none of the validators has correct address
				validators: append([]*types.Validator{me}, valsWithoutAddress...),
				expectedHistory: []mock.HistoryEvent{
					{Operation: mock.OpStop},
					{Operation: mock.OpStop},
				},
			},
		}}

	executeTestCase(t, tc)
}

// TestValidatorConnExecutor_Myself checks what happens if we want to connect to ourselves.
// Expected: connections from previous update are stopped, no new connection established.
func TestValidatorConnExecutor_Myself(t *testing.T) {

	me := mock.NewValidator(mySeedID)

	tc := testCase{
		me: me,
		validatorUpdates: []validatorUpdate{
			0: {
				validators: []*types.Validator{ // new set should have validator 1, 2 and 3
					me,
					mock.NewValidator(1),
					mock.NewValidator(2),
					mock.NewValidator(3),
				},
				expectedHistory: []mock.HistoryEvent{
					{
						Operation: mock.OpDial,
						Params:    []string{mock.NewNodeID(1), mock.NewNodeID(2), mock.NewNodeID(3)},
					},
					{
						Operation: mock.OpDial,
						Params:    []string{mock.NewNodeID(1), mock.NewNodeID(2), mock.NewNodeID(3)},
					},
					{
						Operation: mock.OpDial,
						Params:    []string{mock.NewNodeID(1), mock.NewNodeID(2), mock.NewNodeID(3)},
					},
				},
			},
			1: {
				validators: []*types.Validator{ // new set should have validator 2 and 5
					me,
					mock.NewValidator(1),
					mock.NewValidator(2),
					mock.NewValidator(3),
					mock.NewValidator(4),
					mock.NewValidator(5),
				},
				expectedHistory: []mock.HistoryEvent{
					{Operation: mock.OpStop, Params: []string{mock.NewNodeID(1), mock.NewNodeID(3), mock.NewNodeID(5)}},
					{Operation: mock.OpStop, Params: []string{mock.NewNodeID(1), mock.NewNodeID(3), mock.NewNodeID(5)}},
					{Operation: mock.OpDial, Params: []string{mock.NewNodeID(5)}},
					// {Operation: mock.OpDial, Params: []string{mock.NewNodeID(2), mock.NewNodeID(5)}},
				},
			},
			2: {
				validators: []*types.Validator{me},
				expectedHistory: []mock.HistoryEvent{
					{
						Operation: mock.OpStop,
						Params:    []string{mock.NewNodeID(2), mock.NewNodeID(5)},
					},
					{
						Operation: mock.OpStop,
						Params:    []string{mock.NewNodeID(2), mock.NewNodeID(5)},
					},
				},
			},
		},
	}
	executeTestCase(t, tc)
}

// TestValidatorConnExecutor_EmptyVSet checks what will happen if the ABCI App provides an empty validator set.
// Expected: nothing happens
func TestValidatorConnExecutor_EmptyVSet(t *testing.T) {
	me := mock.NewValidator(mySeedID)
	tc := testCase{
		me: me,
		validatorUpdates: []validatorUpdate{
			0: {
				validators: []*types.Validator{me,
					mock.NewValidator(1),
					mock.NewValidator(2),
					mock.NewValidator(3),
					mock.NewValidator(4),
					mock.NewValidator(5),
				},
				expectedHistory: []mock.HistoryEvent{
					{
						Operation: mock.OpDial,
						Params:    []string{mock.NewNodeID(2), mock.NewNodeID(5)},
					},
					{
						Operation: mock.OpDial,
						Params:    []string{mock.NewNodeID(2), mock.NewNodeID(5)},
					},
				},
			},
			1: {},
		},
	}
	executeTestCase(t, tc)
}

// TestValidatorConnExecutor_ValidatorUpdatesSequence checks sequence of multiple validators switched
func TestValidatorConnExecutor_ValidatorUpdatesSequence(t *testing.T) {
	me := mock.NewValidator(mySeedID)
	tc := testCase{
		me: me,
		validatorUpdates: []validatorUpdate{
			0: {
				validators: []*types.Validator{me,
					mock.NewValidator(1),
					mock.NewValidator(2),
					mock.NewValidator(3),
					mock.NewValidator(4),
				},
				expectedHistory: []mock.HistoryEvent{
					{Operation: mock.OpDial, Params: []string{mock.NewNodeID(1), mock.NewNodeID(2)}},
					{Operation: mock.OpDial, Params: []string{mock.NewNodeID(1), mock.NewNodeID(2)}},
				},
			},
			1: {
				validators: []*types.Validator{
					me,
					mock.NewValidator(2),
					mock.NewValidator(3),
					mock.NewValidator(4),
					mock.NewValidator(5),
				},
				expectedHistory: []mock.HistoryEvent{
					{
						Operation: mock.OpStop,
						Params:    []string{mock.NewNodeID(1)},
					},
					{
						Operation: mock.OpDial,
						Params:    []string{mock.NewNodeID(5)},
					},
				},
			},
			2: { // the same validator set as above, nothing should happen
				validators: []*types.Validator{
					me,
					mock.NewValidator(2),
					mock.NewValidator(3),
					mock.NewValidator(4),
					mock.NewValidator(5),
				},
				expectedHistory: []mock.HistoryEvent{},
			},
			3: { // only 1 validator (except myself), we should stop other validators
				validators: []*types.Validator{
					me,
					mock.NewValidator(1),
				},
				expectedHistory: []mock.HistoryEvent{
					0: {
						Operation: mock.OpStop,
						Params:    []string{mock.NewNodeID(2), mock.NewNodeID(5)},
					},
					1: {
						Operation: mock.OpStop,
						Params:    []string{mock.NewNodeID(2), mock.NewNodeID(5)},
					},
					2: {
						Operation: mock.OpDial,
						Params:    []string{mock.NewNodeID(1)},
					},
				},
			},
			4: { // everything stops
				validators: []*types.Validator{me},
				expectedHistory: []mock.HistoryEvent{
					0: {Operation: mock.OpStop, Params: []string{mock.NewNodeID(1)}},
				},
			},
			5: { // 20 validators; nothing dialed in previous round, so we just dial new validators
				validators: append(mock.NewValidators(20), me),
				expectedHistory: []mock.HistoryEvent{
					{
						Operation: mock.OpDial,
						Params:    []string{mock.NewNodeID(2), mock.NewNodeID(6), mock.NewNodeID(14), mock.NewNodeID(16)},
					},
					{
						Operation: mock.OpDial,
						Params:    []string{mock.NewNodeID(2), mock.NewNodeID(6), mock.NewNodeID(14), mock.NewNodeID(16)},
					},
					{
						Operation: mock.OpDial,
						Params:    []string{mock.NewNodeID(2), mock.NewNodeID(6), mock.NewNodeID(14), mock.NewNodeID(16)},
					},
					{
						Operation: mock.OpDial,
						Params:    []string{mock.NewNodeID(2), mock.NewNodeID(6), mock.NewNodeID(14), mock.NewNodeID(16)},
					},
				},
			},
		},
	}

	executeTestCase(t, tc)
}

// TestEndBlock verifies if ValidatorConnExecutor is called correctly during processing of EndBlock
// message from the ABCI app.
func TestFinalizeBlock(t *testing.T) {
	const timeout = 3 * time.Second // how long we'll wait for connection
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app := newTestApp()
	logger := log.NewTestingLogger(t)

	client := abciclient.NewLocalClient(logger, app)
	require.NotNil(t, client)
	proxyApp := proxy.New(client, logger, proxy.NopMetrics())
	require.NotNil(t, proxyApp)

	err := proxyApp.Start(ctx)
	require.NoError(t, err)

	state, stateDB := makeState(3, 1)
	nodeProTxHash := state.Validators.Validators[0].ProTxHash
	ctx = dash.ContextWithProTxHash(ctx, nodeProTxHash)

	stateStore := sm.NewStore(stateDB)
	blockStore := store.NewBlockStore(dbm.NewMemDB())
	eventBus := eventbus.NewDefault(logger)
	require.NoError(t, eventBus.Start(ctx))

	mp := mocks.NewMempool(t)
	mp.On("Lock").Return()
	mp.On("Unlock").Return()
	mp.On("FlushAppConn", testifymock.Anything).Return(nil)
	mp.On("ReapMaxBytesMaxGas", testifymock.Anything, testifymock.Anything).
		Return(types.Txs{})
	mp.On("Update",
		testifymock.Anything,
		testifymock.Anything,
		testifymock.Anything,
		testifymock.Anything,
		testifymock.Anything,
		testifymock.Anything,
		testifymock.Anything).Return(nil)

	blockExec := sm.NewBlockExecutor(
		stateStore,
		proxyApp,
		mp,
		sm.EmptyEvidencePool{},
		blockStore,
		eventBus,
		sm.BlockExecWithLogger(logger),
	)

	updatesSub, err := eventBus.SubscribeWithArgs(
		ctx,
		pubsub.SubscribeArgs{
			ClientID: "TestEndBlockValidatorUpdates",
			Query:    types.EventQueryValidatorSetUpdates,
		},
	)
	require.NoError(t, err)

	vals := state.Validators
	proTxHashes := vals.GetProTxHashes()
	addProTxHashes := crypto.RandProTxHashes(10)
	proTxHashes = append(proTxHashes, addProTxHashes...)
	newVals, _ := types.GenerateValidatorSet(types.NewValSetParam(proTxHashes))

	// Ensure new validators have some IP addresses set
	for _, validator := range newVals.Validators {
		validator.NodeAddress = types.RandValidatorAddress()
	}

	app.ValidatorSetUpdates[1] = newVals.ABCIEquivalentValidatorUpdates()
	// setup ValidatorConnExecutor
	sw := mock.NewDashDialer()
	proTxHash := newVals.Validators[0].ProTxHash
	vc, err := NewValidatorConnExecutor(proTxHash, eventBus, sw)
	require.NoError(t, err)
	err = vc.Start(ctx)
	require.NoError(t, err)

	block := makeBlock(ctx, t, blockExec, state, 1, new(types.Commit))
	blockID := block.BlockID(nil)
	require.NoError(t, err)
	block.NextValidatorsHash = newVals.Hash()
	const round = int32(0)
	candidateState, err := blockExec.ProcessProposal(ctx, block, round, state, true)
	require.NoError(t, err)

	state, err = blockExec.FinalizeBlock(ctx, state, candidateState, blockID, block, new(types.Commit))
	require.NoError(t, err)

	// test new validator was added to NextValidators
	require.Equal(t, state.Validators.Size(), candidateState.NextValidators.Size())
	nextValidatorsProTxHashes := mock.ValidatorsProTxHashes(candidateState.NextValidators.Validators)
	for _, addProTxHash := range addProTxHashes {
		assert.Contains(t, nextValidatorsProTxHashes, addProTxHash)
	}

	sCtx, sCancel := context.WithTimeout(ctx, 1*time.Second)
	defer sCancel()
	// test we threw an event
	msg, err := updatesSub.Next(sCtx)
	require.NoError(t, err)

	event, ok := msg.Data().(types.EventDataValidatorSetUpdate)
	require.True(
		t,
		ok,
		"Expected event of type EventDataValidatorSetUpdate, got %T",
		msg.Data(),
	)
	if assert.NotEmpty(t, event.ValidatorSetUpdates) {
		for _, addProTxHash := range addProTxHashes {
			assert.Contains(t, mock.ValidatorsProTxHashes(event.ValidatorSetUpdates), addProTxHash)
		}
		assert.EqualValues(
			t,
			types.DefaultDashVotingPower,
			event.ValidatorSetUpdates[1].VotingPower,
		)
		assert.NotEmpty(t, event.QuorumHash)
	}

	// ensure some history got generated inside the Switch; we expect 1 dial event
	select {
	case msg := <-sw.HistoryChan:
		t.Logf("Got message: %s %+v", msg.Operation, msg.Params)
		assert.EqualValues(t, mock.OpDial, msg.Operation)
	case <-time.After(timeout):
		t.Error("Timed out waiting for a history message")
		t.FailNow()
	}
}

// ****** utility functions ****** //

// executeTestCase feeds validator update messages into the event bus
// and ensures operations executed on mock Dash Connection Manager (history records)
// match `expectedHistory`, that is:
// * operation in history record is the same as in `expectedHistory`
// * params in history record are a subset of params in `expectedHistory`
func executeTestCase(t *testing.T, tc testCase) {
	// const TIMEOUT = 100 * time.Millisecond
	const TIMEOUT = 5 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventBus, sw, vc := setup(ctx, t, tc.me)
	defer cleanup(t, eventBus, sw, vc)

	for updateID, update := range tc.validatorUpdates {
		updateEvent := types.EventDataValidatorSetUpdate{
			ValidatorSetUpdates: update.validators,
			QuorumHash:          mock.NewQuorumHash(1000),
		}
		err := eventBus.PublishEventValidatorSetUpdates(updateEvent)
		assert.NoError(t, err)

		// checks
		for checkID, check := range update.expectedHistory {
			select {
			case msg := <-sw.HistoryChan:
				// t.Logf("History event: %+v", msg)
				assert.EqualValues(
					t, check.Operation, msg.Operation,
					"Update %d: wrong operation %s in expected event %d",
					updateID, check.Operation, checkID,
				)
				allowedParams := check.Params
				// if params are nil, we default to all validator addresses; use []string{} to allow no addresses
				if allowedParams == nil {
					allowedParams = allowedParamsDefaults(t, tc, updateID, check, updateEvent.QuorumHash)
				}
				for _, param := range msg.Params {
					// Params of the call need to "contains" only these values as:
					// * we don't dial again already connected validators, and
					// * we randomly select a few validators from new validator set
					assert.Contains(
						t, allowedParams, param,
						"Update %d: wrong params in expected event %d, op %s",
						updateID, checkID, check.Operation,
					)
				}

				// assert.EqualValues(t, check.Params, msg.Params, "check %d", i)
			case <-time.After(TIMEOUT):
				t.Logf("Update %d: timed out waiting for history event %d: %+v", updateID, checkID, check)
				t.FailNow()
			}
		}

		// ensure no new history message arrives, eg. there are no additional operations done on the switch
		select {
		case msg := <-sw.HistoryChan:
			t.Errorf("unexpected history event for update=%d: %+v", updateID, msg)
		case <-time.After(50 * time.Millisecond):
			// this is correct - we time out
		}
	}

}

func allowedParamsDefaults(
	t *testing.T,
	tc testCase,
	updateID int,
	check mock.HistoryEvent,
	quorumHash tmbytes.HexBytes) []string {

	var (
		validators []*types.Validator
	)

	switch check.Operation {
	case mock.OpDial:
		validators = tc.validatorUpdates[updateID].validators
	case mock.OpStop:
		if updateID > 0 {
			validators = tc.validatorUpdates[updateID-1].validators
		}
	}

	selector := selectpeers.NewDIP6ValidatorSelector(quorumHash)
	allowedValidators, err := selector.SelectValidators(validators, tc.me)
	require.NoError(t, err)
	nodeIDs := newValidatorMap(allowedValidators).NodeIDs()
	return nodeIDs
}

// setup creates ValidatorConnExecutor and some dependencies.
// Use `defer cleanup()` to free the resources.
func setup(
	ctx context.Context,
	t *testing.T,
	me *types.Validator,
) (eventBus *eventbus.EventBus, sw *mock.DashDialer, vc *ValidatorConnExecutor) {
	logger := log.NewTestingLogger(t)
	eventBus = eventbus.NewDefault(logger)
	err := eventBus.Start(ctx)
	require.NoError(t, err)

	sw = mock.NewDashDialer()

	vc, err = NewValidatorConnExecutor(me.ProTxHash, eventBus, sw, WithLogger(logger))
	require.NoError(t, err)
	err = vc.Start(ctx)
	require.NoError(t, err)

	return eventBus, sw, vc
}

// cleanup frees some resources allocated for tests
func cleanup(t *testing.T, bus *eventbus.EventBus, dialer p2p.DashDialer, vc *ValidatorConnExecutor) {
	bus.Stop()
	vc.Stop()
}

// SOME UTILS //

func makeState(nVals int, height int64) (sm.State, dbm.DB) {
	genDoc, _ := factory.RandGenesisDoc(nVals, factory.ConsensusParams())
	s, _ := sm.MakeGenesisState(genDoc)

	stateDB := dbm.NewMemDB()
	stateStore := sm.NewStore(stateDB)
	if err := stateStore.Save(s); err != nil {
		panic(err)
	}

	for i := int64(1); i < height; i++ {
		s.LastBlockHeight++
		s.LastValidators = s.Validators.Copy()
		if err := stateStore.Save(s); err != nil {
			panic(err)
		}
	}

	return s, stateDB
}

func makeBlock(ctx context.Context, t *testing.T, blockExec *sm.BlockExecutor, state sm.State, height int64, commit *types.Commit) *types.Block {
	block, crs, err := blockExec.CreateProposalBlock(ctx, 1, 0, state, commit, state.Validators.Proposer.ProTxHash, 1)
	require.NoError(t, err)

	err = crs.UpdateBlock(block)
	require.NoError(t, err)

	return block
}

// TEST APP //

// testApp which changes validators according to updates defined in testApp.ValidatorSetUpdates
type testApp struct {
	abci.BaseApplication

	ByzantineValidators []abci.Misbehavior
	ValidatorSetUpdates map[int64]*abci.ValidatorSetUpdate
}

func newTestApp() *testApp {
	return &testApp{
		ByzantineValidators: []abci.Misbehavior{},
		ValidatorSetUpdates: map[int64]*abci.ValidatorSetUpdate{},
	}
}

var _ abci.Application = (*testApp)(nil)

func (app *testApp) PrepareProposal(_ context.Context, req *abci.RequestPrepareProposal) (*abci.ResponsePrepareProposal, error) {
	txs := types.NewTxs(req.Txs)
	results := factory.ExecTxResults(txs)
	resultsHash, err := abci.TxResultsHash(results)
	if err != nil {
		return nil, err
	}

	return &abci.ResponsePrepareProposal{
		AppHash:   resultsHash,
		TxResults: results,
	}, nil
}

func (app *testApp) ProcessProposal(_ context.Context, req *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
	txs := types.NewTxs(req.Txs)
	results := factory.ExecTxResults(txs)
	resultsHash, err := abci.TxResultsHash(results)
	if err != nil {
		return nil, err
	}

	return &abci.ResponseProcessProposal{
		Status:                abci.ResponseProcessProposal_ACCEPT,
		AppHash:               resultsHash,
		TxResults:             results,
		ConsensusParamUpdates: nil,
		ValidatorSetUpdate:    app.ValidatorSetUpdates[req.Height],
	}, nil
}
