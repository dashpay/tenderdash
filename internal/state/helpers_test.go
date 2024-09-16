package state_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/require"
	dbm "github.com/tendermint/tm-db"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/features/validatorscoring"
	sm "github.com/dashpay/tenderdash/internal/state"
	sf "github.com/dashpay/tenderdash/internal/state/test/factory"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmstate "github.com/dashpay/tenderdash/proto/tendermint/state"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

const (
	chainID = "execution_chain"
)

type paramsChangeTestCase struct {
	height int64
	params types.ConsensusParams
}

func makeAndCommitGoodBlock(
	ctx context.Context,
	t *testing.T,
	state sm.State,
	height int64,
	lastCommit *types.Commit,
	proposerProTxHash crypto.ProTxHash,
	blockExec *sm.BlockExecutor,
	privVals map[string]types.PrivValidator,
	evidence []types.Evidence,
	proposedAppVersion uint64,
) (sm.State, types.BlockID, *types.Commit) {
	t.Helper()
	var err error

	// A good block passes
	state, blockID, block := makeAndApplyGoodBlock(t, state, height, lastCommit, proposerProTxHash, evidence, proposedAppVersion)

	require.NoError(t, blockExec.ValidateBlock(ctx, state, block))
	txResults := factory.ExecTxResults(block.Txs)
	block.ResultsHash, err = abci.TxResultsHash(txResults)
	require.NoError(t, err)

	uncommittedState, err := blockExec.ProcessProposal(ctx, block, 0, state, true)
	require.NoError(t, err)
	// Simulate a lastCommit for this block from all validators for the next height
	commit, _ := makeValidCommit(ctx, t, height, blockID, state.Validators, privVals)
	state, err = blockExec.FinalizeBlock(ctx, state, uncommittedState, blockID, block, commit)
	require.NoError(t, err)

	return state, blockID, commit
}

func makeAndApplyGoodBlock(
	t *testing.T,
	state sm.State,
	height int64,
	lastCommit *types.Commit,
	proposerProTxHash []byte,
	evidence []types.Evidence,
	proposedAppVersion uint64,
) (sm.State, types.BlockID, *types.Block) {
	t.Helper()
	block := state.MakeBlock(height, factory.MakeNTxs(height, 10), lastCommit, evidence, proposerProTxHash, proposedAppVersion)
	partSet, err := block.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)

	blockID := block.BlockID(partSet)
	require.NoError(t, err)

	return state, blockID, block
}

func makeValidCommit(
	ctx context.Context,
	t *testing.T,
	height int64,
	blockID types.BlockID,
	vals *types.ValidatorSet,
	privVals map[string]types.PrivValidator,
) (*types.Commit, []*types.Vote) {
	t.Helper()
	votes := make([]*types.Vote, vals.Size())
	for i := 0; i < vals.Size(); i++ {
		val := vals.GetByIndex(int32(i))
		vote, err := factory.MakeVote(ctx, privVals[val.ProTxHash.String()], vals, chainID, int32(i), height, 0, 2, blockID)
		require.NoError(t, err)
		votes[i] = vote
	}
	thresholdSigns, err := types.NewSignsRecoverer(votes).Recover()
	require.NoError(t, err)
	return types.NewCommit(
		height, 0,
		blockID,
		votes[0].VoteExtensions,
		&types.CommitSigns{
			QuorumSigns: *thresholdSigns,
			QuorumHash:  vals.QuorumHash,
		},
	), votes
}

func makeState(t *testing.T, nVals, height int) (sm.State, dbm.DB, map[string]types.PrivValidator) {
	privValsByProTxHash := make(map[string]types.PrivValidator, nVals)
	vals, privVals := types.RandValidatorSet(nVals)
	genVals := types.MakeGenesisValsFromValidatorSet(vals)
	for i := 0; i < nVals; i++ {
		genVals[i].Name = fmt.Sprintf("test%d", i)
		proTxHash := genVals[i].ProTxHash
		privValsByProTxHash[proTxHash.String()] = privVals[i]
	}
	s, _ := sm.MakeGenesisState(&types.GenesisDoc{
		ChainID:            chainID,
		Validators:         genVals,
		ThresholdPublicKey: vals.ThresholdPublicKey,
		QuorumHash:         vals.QuorumHash,
		QuorumType:         btcjson.LLMQType_5_60,
		AppHash:            make([]byte, crypto.DefaultAppHashSize),
	})

	stateDB := dbm.NewMemDB()
	stateStore := sm.NewStore(stateDB)
	require.NoError(t, stateStore.Save(s))

	for i := 1; i < height; i++ {
		s.LastBlockHeight++
		s.LastValidators = s.Validators.Copy()

		require.NoError(t, stateStore.Save(s))
	}

	return s, stateDB, privValsByProTxHash
}

func makeHeaderPartsResponsesValKeysRegenerate(t *testing.T, state sm.State, regenerate bool, proposedAppVersion uint64) (types.Header, *types.CoreChainLock, types.BlockID, tmstate.ABCIResponses) {
	block, err := sf.MakeBlock(state, state.LastBlockHeight+1, new(types.Commit), proposedAppVersion)
	if err != nil {
		t.Error(err)
	}
	abciResponses := tmstate.ABCIResponses{
		ProcessProposal: &abci.ResponseProcessProposal{
			ValidatorSetUpdate: nil,
			Status:             abci.ResponseProcessProposal_ACCEPT,
		},
	}

	if regenerate == true {
		proTxHashes := state.Validators.GetProTxHashes()
		valUpdates := types.ValidatorUpdatesRegenerateOnProTxHashes(proTxHashes)
		abciResponses.ProcessProposal.ValidatorSetUpdate = &valUpdates
	}
	return block.Header, block.CoreChainLock, types.BlockID{Hash: block.Hash(), PartSetHeader: types.PartSetHeader{}}, abciResponses
}

func makeHeaderPartsResponsesParams(
	t *testing.T,
	state sm.State,
	params *types.ConsensusParams,
	proposedAppVersion uint64,
) (types.Header, *types.CoreChainLock, types.BlockID, *tmstate.ABCIResponses) {
	t.Helper()

	block, err := sf.MakeBlock(state, state.LastBlockHeight+1, new(types.Commit), proposedAppVersion)
	require.NoError(t, err)
	pbParams := params.ToProto()
	abciResponses := &tmstate.ABCIResponses{
		ProcessProposal: &abci.ResponseProcessProposal{
			ConsensusParamUpdates: &pbParams,
			Status:                abci.ResponseProcessProposal_ACCEPT,
		}}
	return block.Header, block.CoreChainLock, types.BlockID{Hash: block.Hash(), PartSetHeader: types.PartSetHeader{}}, abciResponses
}

// used for testing by state store
func makeRandomStateFromValidatorSet(
	lastValSet *types.ValidatorSet,
	height, lastHeightValidatorsChanged int64,
	bs validatorscoring.BlockCommitStore,
) sm.State {
	vs := lastValSet.Copy()
	cp := types.DefaultConsensusParams()
	expectedVS, err := validatorscoring.NewProposerStrategy(*cp, vs, lastHeightValidatorsChanged, 0, bs)
	if err != nil {
		panic(err)
	}
	for h := lastHeightValidatorsChanged; h <= height; h++ {
		if err := expectedVS.UpdateScores(h, 0); err != nil {
			panic(err)
		}
	}

	return sm.State{
		LastBlockHeight:                  height - 1,
		Validators:                       vs.Copy(),
		LastValidators:                   vs.Copy(),
		LastHeightConsensusParamsChanged: height,
		ConsensusParams:                  *cp,
		LastHeightValidatorsChanged:      lastHeightValidatorsChanged,
		InitialHeight:                    1,
	}
}
func makeRandomStateFromConsensusParams(
	_ctx context.Context,
	t *testing.T,
	consensusParams *types.ConsensusParams,
	height,
	lastHeightConsensusParamsChanged int64,
) sm.State {
	t.Helper()
	valSet, _ := types.RandValidatorSet(1)
	return sm.State{
		LastBlockHeight:                  height - 1,
		ConsensusParams:                  *consensusParams,
		LastHeightConsensusParamsChanged: lastHeightConsensusParamsChanged,
		Validators:                       valSet.Copy(),
		LastValidators:                   valSet.Copy(),
		LastHeightValidatorsChanged:      height,
		InitialHeight:                    1,
	}
}

//----------------------------------------------------------------------------

type testApp struct {
	abci.BaseApplication

	Misbehavior        []abci.Misbehavior
	ValidatorSetUpdate *abci.ValidatorSetUpdate
}

var _ abci.Application = (*testApp)(nil)

func (app *testApp) Info(_ context.Context, _req *abci.RequestInfo) (*abci.ResponseInfo, error) {
	return &abci.ResponseInfo{}, nil
}

func (app *testApp) FinalizeBlock(_ context.Context, req *abci.RequestFinalizeBlock) (*abci.ResponseFinalizeBlock, error) {
	app.Misbehavior = req.Misbehavior

	return &abci.ResponseFinalizeBlock{}, nil
}

func (app *testApp) CheckTx(_ context.Context, _req *abci.RequestCheckTx) (*abci.ResponseCheckTx, error) {
	return &abci.ResponseCheckTx{}, nil
}

func (app *testApp) Query(_ context.Context, _req *abci.RequestQuery) (*abci.ResponseQuery, error) {
	return &abci.ResponseQuery{}, nil
}

func (app *testApp) PrepareProposal(_ context.Context, req *abci.RequestPrepareProposal) (*abci.ResponsePrepareProposal, error) {
	resTxs := factory.ExecTxResults(types.NewTxs(req.Txs))
	if resTxs == nil {
		return &abci.ResponsePrepareProposal{}, nil
	}
	return &abci.ResponsePrepareProposal{
		AppHash:            make([]byte, crypto.DefaultAppHashSize),
		ValidatorSetUpdate: app.ValidatorSetUpdate,
		ConsensusParamUpdates: &tmproto.ConsensusParams{
			Version: &tmproto.VersionParams{
				AppVersion: 1,
			},
		},
		AppVersion: 1,
		TxResults:  resTxs,
	}, nil
}

func (app *testApp) ProcessProposal(_ context.Context, req *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
	resTxs := factory.ExecTxResults(types.NewTxs(req.Txs))
	if resTxs == nil {
		return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_REJECT}, nil
	}
	return &abci.ResponseProcessProposal{
		AppHash:            make([]byte, crypto.DefaultAppHashSize),
		ValidatorSetUpdate: app.ValidatorSetUpdate,
		ConsensusParamUpdates: &tmproto.ConsensusParams{
			Version: &tmproto.VersionParams{
				AppVersion: 1,
			},
		},
		TxResults: resTxs,
		Status:    abci.ResponseProcessProposal_ACCEPT,
		Events:    []abci.Event{},
	}, nil
}
