//nolint: lll
package state_test

import (
	"fmt"

	"github.com/tendermint/tendermint/crypto/bls12381"
	dbm "github.com/tendermint/tm-db"

	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/crypto"
	tmrand "github.com/tendermint/tendermint/libs/rand"
	tmstate "github.com/tendermint/tendermint/proto/tendermint/state"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/proxy"
	sm "github.com/tendermint/tendermint/state"
	"github.com/tendermint/tendermint/types"
	tmtime "github.com/tendermint/tendermint/types/time"
)

type paramsChangeTestCase struct {
	height int64
	params tmproto.ConsensusParams
}

func newTestApp() proxy.AppConns {
	app := &testApp{}
	cc := proxy.NewLocalClientCreator(app)
	return proxy.NewAppConns(cc)
}

func makeAndCommitGoodBlock(
	state sm.State,
	nodeProTxHash *crypto.ProTxHash,
	height int64,
	lastCommit *types.Commit,
	proposerProTxHash crypto.ProTxHash,
	blockExec *sm.BlockExecutor,
	privVals map[string]types.PrivValidator,
	evidence []types.Evidence, proposedAppVersion uint64,
) (sm.State, types.BlockID, *types.Commit, error) {

	// A good block passes
	state, blockID, err := makeAndApplyGoodBlock(state, nodeProTxHash, height, lastCommit, proposerProTxHash, blockExec, evidence, proposedAppVersion)
	if err != nil {
		return state, types.BlockID{}, nil, err
	}

	// Simulate a lastCommit for this block from all validators for the next height
	commit, err := makeValidCommit(height, blockID, state.LastStateID, state.Validators, privVals)
	if err != nil {
		return state, types.BlockID{}, nil, err
	}
	return state, blockID, commit, nil
}

func makeAndApplyGoodBlock(state sm.State, nodeProTxHash *crypto.ProTxHash, height int64, lastCommit *types.Commit, proposerProTxHash []byte,
	blockExec *sm.BlockExecutor, evidence []types.Evidence, proposedAppVersion uint64) (sm.State, types.BlockID, error) {
	block, _ := state.MakeBlock(height, nil, makeTxs(height), lastCommit, evidence, proposerProTxHash, proposedAppVersion)
	if err := blockExec.ValidateBlock(state, block); err != nil {
		return state, types.BlockID{}, err
	}
	blockID := types.BlockID{Hash: block.Hash(),
		PartSetHeader: types.PartSetHeader{Total: 3, Hash: tmrand.Bytes(32)}}
	state, _, err := blockExec.ApplyBlock(state, nodeProTxHash, blockID, block)
	if err != nil {
		return state, types.BlockID{}, err
	}

	return state, blockID, nil
}

func makeValidCommit(
	height int64,
	blockID types.BlockID,
	stateID types.StateID,
	vals *types.ValidatorSet,
	privVals map[string]types.PrivValidator,
) (*types.Commit, error) {
	var blockSigs [][]byte
	var stateSigs [][]byte
	var blsIDs [][]byte
	for i := 0; i < vals.Size(); i++ {
		_, val := vals.GetByIndex(int32(i))
		vote, err := types.MakeVote(height, blockID, stateID, vals, privVals[val.ProTxHash.String()], chainID)
		if err != nil {
			return nil, err
		}
		blockSigs = append(blockSigs, vote.BlockSignature)
		stateSigs = append(stateSigs, vote.StateSignature)
		blsIDs = append(blsIDs, vote.ValidatorProTxHash)
	}

	thresholdBlockSig, _ := bls12381.RecoverThresholdSignatureFromShares(blockSigs, blsIDs)
	thresholdStateSig, _ := bls12381.RecoverThresholdSignatureFromShares(stateSigs, blsIDs)

	return types.NewCommit(height, 0, blockID, stateID, vals.QuorumHash, thresholdBlockSig, thresholdStateSig), nil
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

func makeBlock(state sm.State, height int64) *types.Block {
	block, _ := state.MakeBlock(height, nil, makeTxs(state.LastBlockHeight), new(types.Commit), nil, state.Validators.GetProposer().ProTxHash, 0)
	return block
}

func makeHeaderPartsResponsesValKeysRegenerate(state sm.State, regenerate bool) (types.Header, *types.CoreChainLock, types.BlockID, *tmstate.ABCIResponses) {
	block := makeBlock(state, state.LastBlockHeight+1)
	abciResponses := &tmstate.ABCIResponses{
		BeginBlock: &abci.ResponseBeginBlock{},
		EndBlock:   &abci.ResponseEndBlock{ValidatorSetUpdate: nil},
	}
	if regenerate == true {
		proTxHashes := state.Validators.GetProTxHashes()
		valUpdates := types.ValidatorUpdatesRegenerateOnProTxHashes(proTxHashes)
		abciResponses.EndBlock = &abci.ResponseEndBlock{
			ValidatorSetUpdate: &valUpdates,
		}
	}

	return block.Header, block.CoreChainLock, types.BlockID{Hash: block.Hash(), PartSetHeader: types.PartSetHeader{}}, abciResponses
}

// func makeHeaderPartsResponsesValPowerChange(
//	state sm.State,
//	power int64,
// ) (types.Header, *types.ChainLock, types.BlockID, *tmstate.ABCIResponses) {
//
//	block := makeBlock(state, state.LastBlockHeight+1)
//	abciResponses := &tmstate.ABCIResponses{
//		BeginBlock: &abci.ResponseBeginBlock{},
//		EndBlock:   &abci.ResponseEndBlock{ValidatorUpdates: nil},
//	}
//
//	// If the pubkey is new, remove the old and add the new.
//	_, val := state.NextValidators.GetByIndex(0)
//	if val.VotingPower != power {
//		abciResponses.EndBlock = &abci.ResponseEndBlock{
//			ValidatorUpdates: []abci.ValidatorUpdate{
//				types.TM2PB.NewValidatorUpdate(val.PubKey, power, val.ProTxHash),
//			},
//		}
//	}
//
//	return block.Header, block.ChainLock, types.BlockID{Hash: block.Hash(), PartSetHeader: types.PartSetHeader{}}, abciResponses
// }

func makeHeaderPartsResponsesParams(
	state sm.State,
	params tmproto.ConsensusParams,
) (types.Header, *types.CoreChainLock, types.BlockID, *tmstate.ABCIResponses) {

	block := makeBlock(state, state.LastBlockHeight+1)
	abciResponses := &tmstate.ABCIResponses{
		BeginBlock: &abci.ResponseBeginBlock{},
		EndBlock:   &abci.ResponseEndBlock{ConsensusParamUpdates: types.TM2PB.ConsensusParams(&params)},
	}
	return block.Header, block.CoreChainLock, types.BlockID{Hash: block.Hash(), PartSetHeader: types.PartSetHeader{}}, abciResponses
}

func randomGenesisDoc() *types.GenesisDoc {
	pubkey := bls12381.GenPrivKey().PubKey()
	return &types.GenesisDoc{
		GenesisTime: tmtime.Now(),
		ChainID:     "abc",
		Validators: []types.GenesisValidator{
			{
				PubKey:    pubkey,
				ProTxHash: crypto.RandProTxHash(),
				Power:     types.DefaultDashVotingPower,
				Name:      "myval",
			},
		},
		ConsensusParams:    types.DefaultConsensusParams(),
		ThresholdPublicKey: pubkey,
		QuorumHash:         crypto.RandQuorumHash(),
	}
}

//----------------------------------------------------------------------------

type testApp struct {
	abci.BaseApplication

	ByzantineValidators []abci.Evidence
	ValidatorSetUpdate  *abci.ValidatorSetUpdate
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
		ValidatorSetUpdate: app.ValidatorSetUpdate,
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
