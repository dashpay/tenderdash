package dash_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/dash"
	"github.com/tendermint/tendermint/p2p"
	dbm "github.com/tendermint/tm-db"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	abci "github.com/tendermint/tendermint/abci/types"

	"github.com/tendermint/tendermint/libs/log"
	mmock "github.com/tendermint/tendermint/mempool/mock"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/proxy"
	sm "github.com/tendermint/tendermint/state"
	"github.com/tendermint/tendermint/types"
)

var (
	chainID             = "execution_chain"
	testPartSize uint32 = 65536
	nTxsPerBlock        = 10
)

type validatorUpdate struct {
	validators      []*types.Validator
	expectedHistory []mockSwitchHistoryEvent
}
type testCase struct {
	me               *types.Validator
	validatorUpdates []validatorUpdate
}

// TestValidatorConnExecutorNotValidator checks what happens if current node is not a validator.
// Expected: nothing happens
func TestValidatorConnExecutor_NotValidator(t *testing.T) {

	me := deterministicValidator(65535)
	tc := testCase{
		me: me,
		validatorUpdates: []validatorUpdate{
			0: {
				validators: []*types.Validator{
					deterministicValidator(1),
					deterministicValidator(2),
					deterministicValidator(3),
				},
				expectedHistory: []mockSwitchHistoryEvent{},
			}},
	}
	executeTestCase(t, tc)
}

// TestValidatorConnExecutor_WrongAddress checks behavior in case of several issues in the address
func TestValidatorConnExecutor_WrongAddress(t *testing.T) {

	me := deterministicValidator(65535)
	addr1, err := p2p.ParseNodeAddress("http://john@www.google.com:80")
	val1 := &types.Validator{Address: addr1}

	require.NoError(t, err)
	tc := testCase{
		me: me,
		validatorUpdates: []validatorUpdate{
			0: {
				validators: []*types.Validator{
					me,
					deterministicValidator(1),
					deterministicValidator(2),
					deterministicValidator(3),
				},
				expectedHistory: []mockSwitchHistoryEvent{{Operation: "dialMany"}},
			},
			1: {
				validators: []*types.Validator{
					me,
					val1, //nolint:simplifycompositelit
				},
				expectedHistory: []mockSwitchHistoryEvent{
					{Operation: "stopOne"},
					{Operation: "stopOne"}},
			},
		},
	}
	executeTestCase(t, tc)
}

// TestValidatorConnExecutor_Myself checks what happens if we want to connect to ourselves
func TestValidatorConnExecutor_Myself(t *testing.T) {

	me := deterministicValidator(65535)

	tc := testCase{
		me: me,
		validatorUpdates: []validatorUpdate{
			0: {
				validators: []*types.Validator{me},
				// expectedHistory: []mockSwitchHistoryEvent{},
			},
			1: {
				validators: []*types.Validator{
					me,
					deterministicValidator(1),
				},
				expectedHistory: []mockSwitchHistoryEvent{{
					Operation: "dialMany",
				}},
			},
			2: {

				validators: []*types.Validator{
					me,
				},
				expectedHistory: []mockSwitchHistoryEvent{{
					Operation: "stopOne",
					Params:    []string{deterministicValidatorAddress(1)},
				}},
			},
		},
	}
	executeTestCase(t, tc)
}

// TestValidatorConnExecutor_EmptyVSet checks what will happen if the ABCI App provides an empty validator set.
// Expected: nothing happens
func TestValidatorConnExecutor_EmptyVSet(t *testing.T) {

	me := deterministicValidator(65535)
	tc := testCase{
		me: me,
		validatorUpdates: []validatorUpdate{
			0: {
				validators: []*types.Validator{me, deterministicValidator(1)},
				expectedHistory: []mockSwitchHistoryEvent{
					{Operation: "dialMany", Params: []string{me.Address.String(), deterministicValidatorAddress(1)}},
				},
			},
			1: {},
		},
	}
	executeTestCase(t, tc)
}

func TestValidatorConnExecutor_ValidatorUpdatesSequence(t *testing.T) {

	me := deterministicValidator(65535)
	tc := testCase{
		me: me,
		validatorUpdates: []validatorUpdate{
			0: {
				validators: []*types.Validator{me,
					deterministicValidator(1),
				},
				expectedHistory: []mockSwitchHistoryEvent{{Operation: "dialMany"}},
			},
			1: {
				validators: []*types.Validator{
					me,
					deterministicValidator(2),
					deterministicValidator(3),
					deterministicValidator(4),
				},
				expectedHistory: []mockSwitchHistoryEvent{
					{Comment: "Stop old peers that are not part of new validator set", Operation: "stopOne",
						Params: []string{deterministicValidatorAddress(1)},
					},
					{Comment: "Dial two members of current validator set", Operation: "dialMany"},
				},
			},
			2: { // the same as above
				validators: []*types.Validator{
					me,
					deterministicValidator(2),
					deterministicValidator(3),
					deterministicValidator(4),
				},
				expectedHistory: []mockSwitchHistoryEvent{},
			},
			3: {
				validators: []*types.Validator{
					me,
					deterministicValidator(1),
				},
				expectedHistory: []mockSwitchHistoryEvent{
					0: {Operation: "stopOne"},
					1: {Operation: "stopOne"},
					2: {Comment: "Dial new validators",
						Operation: "dialMany",
						Params: []string{
							me.Address.String(),
							deterministicValidatorAddress(1),
						},
					},
				},
			},
		},
	}

	executeTestCase(t, tc)
}

// TestEndBlock verifies if ValidatorConnExecutor is called correctly during processing of EndBlock
// message from the ABCI app.

func TestEndBlock(t *testing.T) {
	const timeout = 3 * time.Second // how long we'll wait for connection
	app := newTestApp()
	cc := proxy.NewLocalClientCreator(app)
	proxyApp := proxy.NewAppConns(cc)
	err := proxyApp.Start()
	require.Nil(t, err)
	defer proxyApp.Stop() //nolint:errcheck // ignore for tests

	state, stateDB, _ := makeState(3, 1)
	nodeProTxHash := &state.Validators.Validators[0].ProTxHash
	stateStore := sm.NewStore(stateDB)

	blockExec := sm.NewBlockExecutor(
		stateStore,
		log.TestingLogger(),
		proxyApp.Consensus(),
		proxyApp.Query(),
		mmock.Mempool{},
		sm.EmptyEvidencePool{},
		nil,
	)

	eventBus := types.NewEventBus()
	err = eventBus.Start()
	require.NoError(t, err)
	defer eventBus.Stop() //nolint:errcheck // ignore for tests

	blockExec.SetEventBus(eventBus)

	updatesSub, err := eventBus.Subscribe(
		context.Background(),
		"TestEndBlockValidatorUpdates",
		types.EventQueryValidatorSetUpdates,
	)
	require.NoError(t, err)

	block := makeBlock(state, 1, new(types.Commit))
	blockID := types.BlockID{
		Hash:          block.Hash(),
		PartSetHeader: block.MakePartSet(testPartSize).Header(),
	}

	vals := state.Validators
	proTxHashes := vals.GetProTxHashes()
	addProTxHash := crypto.RandProTxHash()
	proTxHashes = append(proTxHashes, addProTxHash)
	newVals, _ := types.GenerateValidatorSetUsingProTxHashes(proTxHashes)
	var pos int
	for i, proTxHash := range newVals.GetProTxHashes() {
		if bytes.Equal(proTxHash.Bytes(), addProTxHash.Bytes()) {
			pos = i
		}
	}

	// Ensure new validators have some IP addresses set
	for _, validator := range newVals.Validators {
		validator.Address = p2p.RandNodeAddress()
	}

	// setup ValidatorConnExecutor
	sw := NewMockSwitch()
	nodeID := newVals.Validators[0].Address.NodeID
	vc := dash.NewValidatorConnExecutor(nodeID, eventBus, sw, log.TestingLogger())
	err = vc.Start()
	require.NoError(t, err)
	defer func() { err := vc.Stop(); require.NoError(t, err) }()

	app.ValidatorSetUpdates[1] = newVals.ABCIEquivalentValidatorUpdates()

	state, _, err = blockExec.ApplyBlock(state, nodeProTxHash, blockID, block)
	require.Nil(t, err)
	// test new validator was added to NextValidators
	if assert.Equal(t, state.Validators.Size()+1, state.NextValidators.Size()) {
		idx, _ := state.NextValidators.GetByProTxHash(addProTxHash)
		if idx < 0 {
			t.Fatalf("can't find proTxHash %v in the set %v", addProTxHash, state.NextValidators)
		}
	}
	// test we threw an event
	select {
	case msg := <-updatesSub.Out():
		event, ok := msg.Data().(types.EventDataValidatorSetUpdates)
		require.True(
			t,
			ok,
			"Expected event of type EventDataValidatorSetUpdates, got %T",
			msg.Data(),
		)
		if assert.NotEmpty(t, event.ValidatorUpdates) {
			assert.Equal(t, addProTxHash, event.ValidatorUpdates[pos].ProTxHash)
			assert.EqualValues(
				t,
				types.DefaultDashVotingPower,
				event.ValidatorUpdates[1].VotingPower,
			)
		}
	case <-updatesSub.Cancelled():
		t.Fatalf("updatesSub was cancelled (reason: %v)", updatesSub.Err())
	case <-time.After(1 * time.Second):
		t.Fatal("Did not receive EventValidatorSetUpdates within 1 sec.")
	}

	// ensure some history got generated inside the iSwitch; we expect 1 dial event
	select {
	case msg := <-sw.HistoryChan:
		t.Logf("Got message: %s %+v", msg.Operation, msg.Params)
		assert.EqualValues(t, "dialMany", msg.Operation)
	case <-time.After(timeout):
		t.Error("Timed out waiting for switch history message")
		t.FailNow()
	}
}

// ****** utility functions ****** //

// executeTestCase feeds validator update messages into the event bus
// and ensures operations executed on MockSwitch (history records) match `expectedHistory`, that is:
// * operation in history record is the same as in `expectedHistory`
// * params in history record are a subset of params in `expectedHistory`
func executeTestCase(t *testing.T, tc testCase) {
	// const TIMEOUT = 100 * time.Millisecond
	const TIMEOUT = 5 * time.Second

	eventBus, sw, vc := setup(t, tc.me)
	defer cleanup(t, eventBus, sw, vc)
	// myAddress := strings.TrimPrefix(me.Address.String(), "tcp://")

	for updateID, update := range tc.validatorUpdates {
		updateEvent := types.EventDataValidatorSetUpdates{ValidatorUpdates: update.validators}
		err := eventBus.PublishEventValidatorSetUpdates(updateEvent)
		assert.NoError(t, err)

		// checks
		for checkID, check := range update.expectedHistory {
			select {
			case msg := <-sw.HistoryChan:
				// t.Logf("History event: %+v", msg)
				assert.EqualValues(t, check.Operation, msg.Operation,
					"Update %d: wrong operation %s in expected event %d, comment: %s",
					updateID, check.Operation, checkID, check.Comment)
				allowedParams := check.Params
				// if params are nil, we default to all validator addresses; use []string{} to allow no addresses
				switch check.Operation {
				case "dialMany":
					if allowedParams == nil {
						allowedParams = validatorAddresses(update.validators)
					}
				case "stopOne":
					if allowedParams == nil {
						if updateID > 0 {
							allowedParams = validatorAddresses(tc.validatorUpdates[updateID-1].validators)
						} else {
							t.Error("for 'stop' operation in validator update 0, you need to explicitly provide hisotry Params")
						}
					}

				}
				for _, param := range msg.Params {
					// Params of the call need to "contains" only these values as:
					// * we don't dial again already connected validators, and
					// * we randomly select a few validators from new validator set
					assert.Contains(t, allowedParams, "tcp://"+param,
						"Update %d: wrong params in expected event %d, op %s, comment: %s",
						updateID, checkID, check.Operation, check.Comment)
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
			t.Errorf("unexpected history event: %+v", msg)
		case <-time.After(50 * time.Millisecond):
			// this is correct - we time out
		}
	}

}

// setup creates ValidatorConnExecutor and some dependencies.
// Use `defer cleanup()` to free the resources.
func setup(
	t *testing.T, me *types.Validator) (eventBus *types.EventBus, sw *MockSwitch, vc *dash.ValidatorConnExecutor) {

	eventBus = types.NewEventBus()
	err := eventBus.Start()
	require.NoError(t, err)

	sw = NewMockSwitch()

	nodeID := me.Address.NodeID
	vc = dash.NewValidatorConnExecutor(nodeID, eventBus, sw, log.TestingLogger())
	vc.NumConnections = 2
	err = vc.Start()
	require.NoError(t, err)

	return eventBus, sw, vc
}

// cleanup frees some resources allocated for tests
func cleanup(t *testing.T, bus *types.EventBus, sw dash.ISwitch, vc *dash.ValidatorConnExecutor) {
	assert.NoError(t, bus.Stop())
	assert.NoError(t, vc.Stop())
}

func validatorAddresses(validators []*types.Validator) []string {
	addresses := make([]string, 0, len(validators))
	for _, v := range validators {
		addresses = append(addresses, v.Address.String())
	}

	return addresses
}

// deterministicValidatorAddress generates a string that is accepted as validator address.
// For each `n`, the address will always be the same.
func deterministicValidatorAddress(n uint16) string {
	if n <= 0 {
		panic("n must be > 0")
	}

	nodeID := make([]byte, 20)
	binary.LittleEndian.PutUint16(nodeID, n)

	return fmt.Sprintf("tcp://%x@127.0.0.1:%d", nodeID, n)
}

// deterministicValidator returns new Validator with deterministic address
func deterministicValidator(n uint16) *types.Validator {
	a, _ := p2p.ParseNodeAddress(deterministicValidatorAddress(n))
	return &types.Validator{Address: a}
}

// Utility functions //

// make some bogus txs
func makeTxs(height int64) (txs []types.Tx) {
	for i := 0; i < nTxsPerBlock; i++ {
		txs = append(txs, types.Tx([]byte{byte(height), byte(i)}))
	}
	return txs
}

func makeState(nVals int, height int64) (sm.State, dbm.DB, map[string]types.PrivValidator) {
	privValsByProTxHash := make(map[string]types.PrivValidator, nVals)
	vals, privVals, quorumHash, thresholdPublicKey := types.GenerateMockGenesisValidators(nVals)
	// vals and privals are sorted
	for i := 0; i < nVals; i++ {
		vals[i].Name = fmt.Sprintf("test%d", i)
		proTxHash := vals[i].ProTxHash
		privValsByProTxHash[proTxHash.String()] = privVals[i]
	}
	s, _ := sm.MakeGenesisState(&types.GenesisDoc{
		ChainID:            chainID,
		Validators:         vals,
		ThresholdPublicKey: thresholdPublicKey,
		QuorumHash:         quorumHash,
		AppHash:            nil,
	})

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

	return s, stateDB, privValsByProTxHash
}

func makeBlock(state sm.State, height int64, commit *types.Commit) *types.Block {
	block, _ := state.MakeBlock(height, nil, makeTxs(state.LastBlockHeight),
		commit, nil, state.Validators.GetProposer().ProTxHash, 0)
	return block
}

// TEST APP //

// testApp just changes validators every Nth block
type testApp struct {
	abci.BaseApplication

	ByzantineValidators []abci.Evidence
	ValidatorSetUpdates map[int64]*abci.ValidatorSetUpdate
}

func newTestApp() *testApp {
	return &testApp{
		ByzantineValidators: []abci.Evidence{},
		ValidatorSetUpdates: map[int64]*abci.ValidatorSetUpdate{},
	}
}

var _ abci.Application = (*testApp)(nil)

func (app *testApp) Info(req abci.RequestInfo) (resInfo abci.ResponseInfo) {
	return abci.ResponseInfo{}
}

func (app *testApp) BeginBlock(req abci.RequestBeginBlock) abci.ResponseBeginBlock {
	app.ByzantineValidators = req.ByzantineValidators
	return abci.ResponseBeginBlock{}
}

func (app *testApp) EndBlock(req abci.RequestEndBlock) abci.ResponseEndBlock {
	return abci.ResponseEndBlock{
		ValidatorSetUpdate: app.ValidatorSetUpdates[req.Height],
		ConsensusParamUpdates: &abci.ConsensusParams{
			Version: &tmproto.VersionParams{
				AppVersion: 1}}}
}

func (app *testApp) DeliverTx(req abci.RequestDeliverTx) abci.ResponseDeliverTx {
	return abci.ResponseDeliverTx{Events: []abci.Event{}}
}

func (app *testApp) CheckTx(req abci.RequestCheckTx) abci.ResponseCheckTx {
	return abci.ResponseCheckTx{}
}

func (app *testApp) Commit() abci.ResponseCommit {
	return abci.ResponseCommit{RetainHeight: 1}
}

func (app *testApp) Query(reqQuery abci.RequestQuery) (resQuery abci.ResponseQuery) {
	return
}
