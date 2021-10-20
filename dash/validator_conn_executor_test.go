package dash_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tendermint/tendermint/config"
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

func newValidatorSet(nvals int) (*types.ValidatorSet, []types.PrivValidator) {
	vs, privval := types.GenerateValidatorSet(nvals)
	for i, val := range vs.Validators {
		addr, err := types.NewValidatorAddress(fmt.Sprintf("tcp://%s@127.0.0.1:%d", p2p.PubKeyToID(val.PubKey), i+1))
		if err != nil {
			panic(err)
		}
		val.Address = addr
	}
	return vs, privval
}

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

func cleanup(t *testing.T, bus *types.EventBus, sw dash.ISwitch, vc *dash.ValidatorConnExecutor) {
	assert.NoError(t, bus.Stop())
	assert.NoError(t, vc.Stop())
}

// Test scenarios:
// 1. Empty ValidatorSet
// 2. ValidatorSet is not changed
func testValidatorConnExecutor(t *testing.T,
	me *types.Validator,
	validatorSequence [][]*types.Validator,
	expectedHistory []mockSwitchHistoryEvent) {

	eventBus, sw, vc := setup(t, me)
	defer cleanup(t, eventBus, sw, vc)
	myAddress := strings.TrimPrefix(me.Address.String(), "tcp://")
	for _, validators := range validatorSequence {
		updateEvent := types.EventDataValidatorSetUpdates{ValidatorUpdates: append(validators, me)}
		err := eventBus.PublishEventValidatorSetUpdates(updateEvent)
		assert.NoError(t, err)
	}

	for i, check := range expectedHistory {
		select {
		case msg := <-sw.HistoryChan:
			t.Logf("History event: %+v", msg)
			assert.EqualValues(t, check.Operation, msg.Operation, "check %d", i)
			// we "dial" only members of Validator Set
			if check.Operation == "dial" {
				for _, param := range msg.Params {
					assert.Contains(t, append(check.Params, myAddress), "tcp://"+param,
						"Params check %d, op %s", i, check.Operation)
				}
			}
			// assert.EqualValues(t, check.Params, msg.Params, "check %d", i)
		case <-time.After(3 * time.Second):
			t.Logf("Timed out waiting for history event %d: %+v", i, check)
			t.FailNow()
		}
	}

}

func deterministicValidatorAddress(n int) types.ValidatorAddress {
	nodeID := make([]byte, 20)
	binary.LittleEndian.PutUint64(nodeID, uint64(n))

	a, _ := types.NewValidatorAddress(fmt.Sprintf("tcp://%x@127.0.0.1:%d", nodeID, n))
	return a
}

// func validatorAddresses(validators []*types.Validator) []string {
// 	addresses := make([]string, 0, len(validators))
// 	for _, v := range validators {
// 		addresses = append(addresses, strings.TrimPrefix(v.Address.String(), "tcp://"))
// 	}

// 	return addresses
// }
func TestValidatorConnExecutor_Cases(t *testing.T) {
	type testCase struct {
		name            string
		me              *types.Validator
		validatorsSeq   [][]*types.Validator
		expectedHistory []mockSwitchHistoryEvent
	}

	me := &types.Validator{Address: deterministicValidatorAddress(65535)}
	testcases := []testCase{
		{
			name: "empty validators",
			me:   me,
			validatorsSeq: [][]*types.Validator{
				{ // rotation 0 - initial validator set
					me,
					&types.Validator{Address: deterministicValidatorAddress(1)},
				},
				{ // rotation 1
					me,
					&types.Validator{Address: deterministicValidatorAddress(2)},
					&types.Validator{Address: deterministicValidatorAddress(3)},
					&types.Validator{Address: deterministicValidatorAddress(4)},
				},
				{ // rotation 2
					me,
					&types.Validator{Address: deterministicValidatorAddress(1)},
				},
			},
			expectedHistory: []mockSwitchHistoryEvent{
				// Operations for rotation 0 (validatorsSeq[0])
				{
					Operation: "dial",
					// Params of the call need to "contains" only these values
					Params: []string{me.Address.String(), deterministicValidatorAddress(1).String()},
				},
				// Operations for rotation 1 (validatorsSeq[1])
				{ // Stop old peers that are not part of new validator set
					Operation: "stop",
					Params:    []string{deterministicValidatorAddress(1).String()},
				},
				{ // Dial two members of validator set
					Operation: "dial",
					Params: []string{
						me.Address.String(),
						deterministicValidatorAddress(2).String(),
						deterministicValidatorAddress(3).String(),
						deterministicValidatorAddress(4).String(),
					},
				},
				// Operations for rotation 1 (validatorsSeq[1])
				{
					// Stop previous peers which are not part of new ValidatorSet
					Operation: "stop",
					Params: []string{
						deterministicValidatorAddress(2).String(),
						deterministicValidatorAddress(3).String(),
						deterministicValidatorAddress(4).String(),
					},
				},
				{
					Operation: "dial",
					Params: []string{
						me.Address.String(),
						deterministicValidatorAddress(1).String(),
					},
				},
			},
		}}

	// nolint:scopelint
	for _, tt := range testcases {
		t.Run(tt.name, func(t *testing.T) {
			testValidatorConnExecutor(t, tt.me, tt.validatorsSeq, tt.expectedHistory)
		})
	}
}

// TODO does not work, fix or remove
func __TestValidatorConnExecutor(t *testing.T) {
	wait := 3 * time.Second // how long we'll wait for connection
	nBlocks := 10
	nVals := 5

	app := newTestApp()
	privVals := map[int64][]types.PrivValidator{}
	for height := int64(2); height <= int64(nBlocks); height++ {
		vu, pv := newValidatorSet(nVals)
		app.ValidatorSetUpdates[height] = vu.ABCIEquivalentValidatorUpdates()
		privVals[height] = pv
	}

	cc := proxy.NewLocalClientCreator(app)
	proxyApp := proxy.NewAppConns(cc)
	err := proxyApp.Start()
	require.Nil(t, err)
	defer proxyApp.Stop() //nolint:errcheck // ignore for tests

	state, stateDB, pv := makeState(1, 1)

	nodeProTxHash := &state.Validators.Validators[0].ProTxHash
	privVals[1] = []types.PrivValidator{pv[nodeProTxHash.String()]}

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

	sw := NewMockSwitch()
	nodeID := state.Validators.Validators[0].Address.NodeID
	vc := dash.NewValidatorConnExecutor(nodeID, eventBus, sw, log.TestingLogger())
	vc.NumConnections = 2
	err = vc.Start()
	require.NoError(t, err)
	defer func() { err := vc.Stop(); require.NoError(t, err) }()

	lastCommit := new(types.Commit)
	for i := 0; i < nBlocks; i++ {
		height := state.LastBlockHeight + 1

		block := makeBlock(state, height, lastCommit)
		block.LastCommit = lastCommit
		block.LastCommitHash = lastCommit.Hash()

		blockID := types.BlockID{
			Hash:          block.Hash(),
			PartSetHeader: block.MakePartSet(testPartSize).Header(),
		}

		// vs := types.NewVoteSet(chainID, height, 0, tmproto.CommitType, state.Validators, state.StateID())
		// lastCommit, err = types.MakeCommit(blockID, state.StateID(), height, 0, vs, privVals[height])
		// assert.NoError(t, err)
		lastCommit = &types.Commit{
			Height: height,
		}
		state, _, err = blockExec.ApplyBlock(state, nodeProTxHash, blockID, block)
		require.Nil(t, err, "height=%d, i=%d", height, i)
	}

	time.Sleep(wait)

	// t.Logf("History: %+v", sw.History)
}

// TestEndBlockValidatorConnect ensures we connect to new validators after update of Validator Set
// TODO does not work
func __TestEndBlockValidatorConnect(t *testing.T) {
	wait := 15 * time.Second // how long we'll wait for connection
	app := newTestApp()
	cc := proxy.NewLocalClientCreator(app)
	proxyApp := proxy.NewAppConns(cc)
	err := proxyApp.Start()
	require.Nil(t, err)
	defer proxyApp.Stop() //nolint:errcheck // ignore for tests

	state, stateDB, _ := makeState(1, 1)
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

	sw := createSwitch(t, 1)
	nodeID := state.Validators.Validators[0].Address.NodeID
	vc := dash.NewValidatorConnExecutor(nodeID, eventBus, sw, log.TestingLogger())
	err = vc.Start()
	require.NoError(t, err)
	defer func() { err := vc.Stop(); require.NoError(t, err) }()

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
		validator.Address = types.RandValidatorAddress()
	}

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
	time.Sleep(wait)
}

// Utility functions //

// Creates a switch with the provided config
func createSwitch(t *testing.T, id int) *p2p.Switch {
	cfg := config.P2PConfig{}
	peer := p2p.MakeSwitch(
		&cfg,
		id,
		"127.0.0.1",
		"123.123.123",
		nil,
		func(i int, sw *p2p.Switch) *p2p.Switch {
			sw.SetLogger(log.TestingLogger())
			return sw
		},
	)
	return peer
}

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
