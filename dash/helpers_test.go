package dash

import (
	"encoding/binary"
	"fmt"

	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/libs/bytes"
	"github.com/tendermint/tendermint/p2p"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	sm "github.com/tendermint/tendermint/state"
	"github.com/tendermint/tendermint/types"
	dbm "github.com/tendermint/tm-db"
)

// mockNodeAddress generates a string that is accepted as validator address.
// For given `n`, the address will always be the same.
func mockNodeAddress(n uint64) string {
	nodeID := make([]byte, 20)
	binary.LittleEndian.PutUint64(nodeID, n)

	return fmt.Sprintf("tcp://%x@127.0.0.1:%d", nodeID, uint16(n))
}

// mockValidator generates a validator with only fields needed for node selection filled.
// For the same `id`, mock validator will always have the same data (proTxHash, NodeID)
func mockValidator(id uint64) *types.Validator {
	address, _ := p2p.ParseNodeAddress(mockNodeAddress(id))

	return &types.Validator{
		ProTxHash:   mockProTxHash(id),
		NodeAddress: address,
	}
}

// mockValidators generates a slice containing `n` mock validators.
// Each element is generated using `mockValidator()`.
func mockValidators(n uint64) []*types.Validator {
	vals := make([]*types.Validator, 0, n)
	for i := uint64(0); i < n; i++ {
		vals = append(vals, mockValidator(i))
	}
	return vals
}

// mockProTxHash generates a deterministic proTxHash.
// For the same `id`, generated data is always the same.
func mockProTxHash(id uint64) []byte {
	data := make([]byte, crypto.ProTxHashSize)
	binary.LittleEndian.PutUint64(data, id)
	return data
}

// mockQuorumHash generates a deterministic quorum hash.
// For the same `id`, generated data is always the same.
func mockQuorumHash(id uint64) []byte {
	data := make([]byte, crypto.QuorumHashSize)
	binary.LittleEndian.PutUint64(data, id)
	return data
}

// mockProTxHashes generates multiple deterministic proTxHash'es using mockProTxHash.
// Each argument will be passed to mockProTxHash.
func mockProTxHashes(ids ...uint64) []bytes.HexBytes {
	hashes := make([]bytes.HexBytes, 0, len(ids))
	for _, id := range ids {
		hashes = append(hashes, mockProTxHash(id))
	}
	return hashes
}

// validatorsProTxHashes returns slice of proTxHashes for provided list of validators
func validatorsProTxHashes(vals []*types.Validator) []bytes.HexBytes {
	hashes := make([]bytes.HexBytes, len(vals))
	for id, val := range vals {
		hashes[id] = val.ProTxHash
	}
	return hashes
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

// testApp which changes validators according to updates defined in testApp.ValidatorSetUpdates
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
