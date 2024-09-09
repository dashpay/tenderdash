package state_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	dbm "github.com/tendermint/tm-db"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	abciclientmocks "github.com/dashpay/tenderdash/abci/client/mocks"
	abci "github.com/dashpay/tenderdash/abci/types"
	abcimocks "github.com/dashpay/tenderdash/abci/types/mocks"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	"github.com/dashpay/tenderdash/crypto/encoding"
	"github.com/dashpay/tenderdash/dash"
	"github.com/dashpay/tenderdash/internal/eventbus"
	mpmocks "github.com/dashpay/tenderdash/internal/mempool/mocks"
	"github.com/dashpay/tenderdash/internal/proxy"
	"github.com/dashpay/tenderdash/internal/pubsub"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/state/mocks"
	sf "github.com/dashpay/tenderdash/internal/state/test/factory"
	"github.com/dashpay/tenderdash/internal/store"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/rand"
	tmtypes "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

var (
	testPartSize uint32 = 65536
)

func TestApplyBlock(t *testing.T) {
	app := &testApp{}
	logger := log.NewNopLogger()
	cc := abciclient.NewLocalClient(logger, app)
	proxyApp := proxy.New(cc, logger, proxy.NopMetrics())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, proxyApp.Start(ctx))

	eventBus := eventbus.NewDefault(logger)
	require.NoError(t, eventBus.Start(ctx))

	state, stateDB, _ := makeState(t, 1, 1)
	stateStore := sm.NewStore(stateDB)
	// The state is local, so we just take the first proTxHash
	nodeProTxHash := state.Validators.Validators[0].ProTxHash
	ctx = dash.ContextWithProTxHash(ctx, nodeProTxHash)

	app.ValidatorSetUpdate = state.Validators.ABCIEquivalentValidatorUpdates()

	blockStore := store.NewBlockStore(dbm.NewMemDB())
	mp := &mpmocks.Mempool{}
	mp.On("Lock").Return()
	mp.On("Unlock").Return()
	mp.On("FlushAppConn", mock.Anything).Return(nil)
	mp.On("Update",
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything).Return(nil)
	blockExec := sm.NewBlockExecutor(
		stateStore,
		proxyApp,
		mp,
		sm.EmptyEvidencePool{},
		blockStore,
		eventBus,
	)

	// Consensus params, version and validators shall be applied in next block.
	consensusParamsBefore := state.ConsensusParams
	validatorsBefore := state.Validators.Hash()

	block, err := sf.MakeBlock(state, 1, new(types.Commit), 2)
	require.NoError(t, err)
	bps, err := block.MakePartSet(testPartSize)
	require.NoError(t, err)
	blockID := types.BlockID{Hash: block.Hash(), PartSetHeader: bps.Header()}

	state, err = blockExec.ApplyBlock(ctx, state, blockID, block, new(types.Commit))
	require.NoError(t, err)

	// State for next block
	// nextState, err := state.NewStateChangeset(ctx, nil)
	// require.NoError(t, err)
	assert.EqualValues(t, 1, block.Version.App, "App version should not change in current block")
	assert.EqualValues(t, 2, block.ProposedAppVersion, "Block should propose new version")

	assert.Equal(t, consensusParamsBefore.HashConsensusParams(), block.ConsensusHash, "consensus params should change in next block")
	assert.Equal(t, validatorsBefore, block.ValidatorsHash, "validators should change from the next block")

	// TODO check state and mempool
	assert.EqualValues(t, 1, state.Version.Consensus.App, "App version wasn't updated")
}

// TestFinalizeBlockByzantineValidators ensures we send byzantine validators list.
func TestFinalizeBlockByzantineValidators(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app := &testApp{}
	logger := log.NewNopLogger()
	cc := abciclient.NewLocalClient(logger, app)
	proxyApp := proxy.New(cc, logger, proxy.NopMetrics())
	err := proxyApp.Start(ctx)
	require.NoError(t, err)

	state, stateDB, privVals := makeState(t, 1, 1)
	stateStore := sm.NewStore(stateDB)
	nodeProTxHash := state.Validators.Validators[0].ProTxHash
	ctx = dash.ContextWithProTxHash(ctx, nodeProTxHash)
	app.ValidatorSetUpdate = state.Validators.ABCIEquivalentValidatorUpdates()

	defaultEvidenceTime := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)
	privVal := privVals[state.Validators.Validators[0].ProTxHash.String()]

	// we don't need to worry about validating the evidence as long as they pass validate basic
	dve, err := types.NewMockDuplicateVoteEvidenceWithValidator(
		ctx,
		3,
		defaultEvidenceTime,
		privVal,
		state.ChainID,
		state.Validators.QuorumType,
		state.Validators.QuorumHash,
	)
	require.NoError(t, err)
	dve.ValidatorPower = types.DefaultDashVotingPower

	ev := []types.Evidence{dve}

	abciMb := []abci.Misbehavior{
		{
			Type:             abci.MisbehaviorType_DUPLICATE_VOTE,
			Height:           3,
			Time:             defaultEvidenceTime,
			Validator:        types.TM2PB.Validator(state.Validators.Validators[0]),
			TotalVotingPower: types.DefaultDashVotingPower,
		},
	}

	evpool := &mocks.EvidencePool{}
	evpool.On("PendingEvidence", mock.AnythingOfType("int64")).Return(ev, int64(100))
	evpool.On("Update", ctx, mock.AnythingOfType("state.State"), mock.AnythingOfType("types.EvidenceList")).Return()
	evpool.On("CheckEvidence", ctx, mock.AnythingOfType("types.EvidenceList")).Return(nil)
	mp := &mpmocks.Mempool{}
	mp.On("Lock").Return()
	mp.On("Unlock").Return()
	mp.On("FlushAppConn", mock.Anything).Return(nil)
	mp.On("Update",
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything).Return(nil)

	eventBus := eventbus.NewDefault(logger)
	require.NoError(t, eventBus.Start(ctx))

	blockStore := store.NewBlockStore(dbm.NewMemDB())

	blockExec := sm.NewBlockExecutor(stateStore, proxyApp, mp, evpool, blockStore, eventBus)

	block, err := sf.MakeBlock(state, 1, new(types.Commit), 1)
	block.SetDashParams(0, nil, block.ProposedAppVersion, nil)
	require.NoError(t, err)
	block.Evidence = ev
	block.Header.EvidenceHash = block.Evidence.Hash()
	bps, err := block.MakePartSet(testPartSize)
	require.NoError(t, err)

	blockID := types.BlockID{Hash: block.Hash(), PartSetHeader: bps.Header()}

	_, err = blockExec.ApplyBlock(ctx, state, blockID, block, new(types.Commit))
	require.NoError(t, err)

	// TODO check state and mempool
	assert.Equal(t, abciMb, app.Misbehavior)
}

func TestProcessProposal(t *testing.T) {
	const (
		height = 1
		// just some arbitrary round, to ensure everything works correctly
		round = int32(12)
	)
	txs := factory.MakeNTxs(height, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app := abcimocks.NewApplication(t)
	logger := log.NewNopLogger()
	cc := abciclient.NewLocalClient(logger, app)
	proxyApp := proxy.New(cc, logger, proxy.NopMetrics())
	err := proxyApp.Start(ctx)
	require.NoError(t, err)

	state, stateDB, _ := makeState(t, 1, height)
	stateStore := sm.NewStore(stateDB)
	blockStore := store.NewBlockStore(dbm.NewMemDB())

	eventBus := eventbus.NewDefault(logger)
	require.NoError(t, eventBus.Start(ctx))

	blockExec := sm.NewBlockExecutor(
		stateStore,
		proxyApp,
		new(mpmocks.Mempool),
		sm.EmptyEvidencePool{},
		blockStore,
		eventBus,
	)

	coreChainLockUpdate := types.CoreChainLock{
		CoreBlockHeight: 3000,
		CoreBlockHash:   make([]byte, 32),
		Signature:       make([]byte, bls12381.SignatureSize),
	}

	lastCommit := types.NewCommit(height-1, 0, types.BlockID{}, nil, nil)
	block1, err := sf.MakeBlock(state, height, lastCommit, 1)
	require.NoError(t, err)
	block1.SetCoreChainLock(&coreChainLockUpdate)
	block1.Txs = txs
	txResults := factory.ExecTxResults(txs)
	block1.ResultsHash, err = abci.TxResultsHash(txResults)
	require.NoError(t, err)

	version := block1.Version.ToProto()

	expectedRpp := &abci.RequestProcessProposal{
		Txs:         block1.Txs.ToSliceOfBytes(),
		Hash:        block1.Hash(),
		Height:      block1.Header.Height,
		Round:       round,
		Time:        block1.Header.Time,
		Misbehavior: block1.Evidence.ToABCI(),
		ProposedLastCommit: abci.CommitInfo{
			Round: 0,
			//QuorumHash:
			//BlockSignature:
			//StateSignature:
		},
		CoreChainLockUpdate:   block1.CoreChainLock.ToProto(),
		CoreChainLockedHeight: block1.CoreChainLock.CoreBlockHeight,
		NextValidatorsHash:    block1.NextValidatorsHash,
		ProposerProTxHash:     block1.ProposerProTxHash,
		Version:               &version,
		ProposedAppVersion:    1,
		QuorumHash:            state.Validators.QuorumHash,
	}

	app.On("ProcessProposal", mock.Anything, mock.Anything).Return(&abci.ResponseProcessProposal{
		AppHash:   block1.AppHash,
		TxResults: txResults,
		Status:    abci.ResponseProcessProposal_ACCEPT,
	}, nil)
	uncommittedState, err := blockExec.ProcessProposal(ctx, block1, round, state, true)
	require.NoError(t, err)
	assert.NotZero(t, uncommittedState)
	app.AssertExpectations(t)
	app.AssertCalled(t, "ProcessProposal", ctx, expectedRpp)
}

func TestUpdateConsensusParams(t *testing.T) {
	const (
		height = 1
		round  = int32(12)
	)
	txs := factory.MakeNTxs(height, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app := abcimocks.NewApplication(t)
	logger := log.NewNopLogger()
	cc := abciclient.NewLocalClient(logger, app)
	proxyApp := proxy.New(cc, logger, proxy.NopMetrics())
	err := proxyApp.Start(ctx)
	require.NoError(t, err)

	state, stateDB, _ := makeState(t, 1, height)
	stateStore := sm.NewStore(stateDB)
	blockStore := store.NewBlockStore(dbm.NewMemDB())

	eventBus := eventbus.NewDefault(logger)
	require.NoError(t, eventBus.Start(ctx))

	pool := new(mpmocks.Mempool)
	pool.On("ReapMaxBytesMaxGas", mock.Anything, mock.Anything).Return(txs).Once()

	blockExec := sm.NewBlockExecutor(
		stateStore,
		proxyApp,
		pool,
		sm.EmptyEvidencePool{},
		blockStore,
		eventBus,
	)

	lastCommit := types.NewCommit(height-1, 0, types.BlockID{}, nil, nil)
	txResults := factory.ExecTxResults(txs)

	app.On("PrepareProposal", mock.Anything, mock.Anything).Return(&abci.ResponsePrepareProposal{
		TxRecords:             txsToTxRecords(txs),
		AppHash:               rand.Bytes(crypto.DefaultAppHashSize),
		TxResults:             txResults,
		ConsensusParamUpdates: &tmtypes.ConsensusParams{Block: &tmtypes.BlockParams{MaxBytes: 1024 * 1024}},
		AppVersion:            1,
	}, nil).Once()
	prop := state.ProposerSelector().MustGetProposer(height, round)
	block1, _, err := blockExec.CreateProposalBlock(
		ctx,
		height,
		round,
		state,
		lastCommit,
		prop.ProTxHash,
		1,
	)
	require.NoError(t, err)

	app.On("ProcessProposal", mock.Anything, mock.Anything).Return(&abci.ResponseProcessProposal{
		AppHash:               block1.AppHash,
		TxResults:             txResults,
		Status:                abci.ResponseProcessProposal_ACCEPT,
		ConsensusParamUpdates: &tmtypes.ConsensusParams{Block: &tmtypes.BlockParams{MaxBytes: 1024 * 1024}},
	}, nil).Once()
	uncommittedState, err := blockExec.ProcessProposal(ctx, block1, round, state, true)
	require.NoError(t, err)
	assert.Equal(t, block1.NextConsensusHash, uncommittedState.NextConsensusParams.HashConsensusParams())

	app.AssertExpectations(t)
	app.AssertCalled(t, "ProcessProposal", mock.Anything, mock.Anything)
}

// TestOverrideAppVersion ensures that app_version set in PrepareProposal overrides the one in the block
// and is passed to ProcessProposal.
func TestOverrideAppVersion(t *testing.T) {
	const (
		height     = 1
		round      = int32(12)
		appVersion = uint64(12345)
	)
	txs := factory.MakeNTxs(height, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app := abcimocks.NewApplication(t)
	logger := log.NewNopLogger()
	cc := abciclient.NewLocalClient(logger, app)
	proxyApp := proxy.New(cc, logger, proxy.NopMetrics())
	err := proxyApp.Start(ctx)
	require.NoError(t, err)

	state, stateDB, _ := makeState(t, 1, height)
	stateStore := sm.NewStore(stateDB)
	blockStore := store.NewBlockStore(dbm.NewMemDB())

	eventBus := eventbus.NewDefault(logger)
	require.NoError(t, eventBus.Start(ctx))

	pool := new(mpmocks.Mempool)
	pool.On("ReapMaxBytesMaxGas", mock.Anything, mock.Anything).Return(txs).Once()

	blockExec := sm.NewBlockExecutor(
		stateStore,
		proxyApp,
		pool,
		sm.EmptyEvidencePool{},
		blockStore,
		eventBus,
	)

	lastCommit := types.NewCommit(height-1, 0, types.BlockID{}, nil, nil)
	txResults := factory.ExecTxResults(txs)

	app.On("PrepareProposal", mock.Anything, mock.Anything).Return(&abci.ResponsePrepareProposal{
		TxRecords:  txsToTxRecords(txs),
		AppHash:    rand.Bytes(crypto.DefaultAppHashSize),
		TxResults:  txResults,
		AppVersion: appVersion,
	}, nil).Once()

	prop := state.ProposerSelector().MustGetProposer(height, round)
	block1, _, err := blockExec.CreateProposalBlock(
		ctx,
		height,
		round,
		state,
		lastCommit,
		prop.ProTxHash,
		1,
	)
	require.NoError(t, err)

	app.On("ProcessProposal", mock.Anything,
		mock.MatchedBy(func(r *abci.RequestProcessProposal) bool {
			return r.Version.App == appVersion
		})).Return(&abci.ResponseProcessProposal{
		AppHash:   block1.AppHash,
		TxResults: txResults,
		Status:    abci.ResponseProcessProposal_ACCEPT,
	}, nil).Once()

	_, err = blockExec.ProcessProposal(ctx, block1, round, state, true)
	require.NoError(t, err)
	assert.EqualValues(t, appVersion, block1.Version.App, "App version should be overridden by PrepareProposal")

	app.AssertExpectations(t)
}

func TestValidateValidatorUpdates(t *testing.T) {
	pubkey1 := bls12381.GenPrivKey().PubKey()
	pubkey2 := bls12381.GenPrivKey().PubKey()
	pk1, err := encoding.PubKeyToProto(pubkey1)
	assert.NoError(t, err)
	pk2, err := encoding.PubKeyToProto(pubkey2)
	assert.NoError(t, err)

	proTxHash1 := crypto.RandProTxHash()
	proTxHash2 := crypto.RandProTxHash()

	defaultValidatorParams := types.ValidatorParams{
		PubKeyTypes: []string{types.ABCIPubKeyTypeBLS12381},
	}

	addr := types.RandValidatorAddress()

	testCases := []struct {
		name string

		abciUpdates     []abci.ValidatorUpdate
		validatorParams types.ValidatorParams

		shouldErr bool
	}{
		{
			"adding a validator is OK",
			[]abci.ValidatorUpdate{{PubKey: &pk2, Power: 100, ProTxHash: proTxHash2, NodeAddress: addr.String()}},
			defaultValidatorParams,
			false,
		},
		{"adding a validator without address is OK",
			[]abci.ValidatorUpdate{{PubKey: &pk2, Power: 100, ProTxHash: proTxHash2}},
			defaultValidatorParams,
			false,
		},
		{
			"updating a validator is OK",
			[]abci.ValidatorUpdate{{PubKey: &pk1, Power: 100, ProTxHash: proTxHash1, NodeAddress: addr.String()}},
			defaultValidatorParams,
			false,
		},
		{
			"removing a validator is OK",
			[]abci.ValidatorUpdate{{Power: 0, ProTxHash: proTxHash2, NodeAddress: addr.String()}},
			defaultValidatorParams,
			false,
		},
		{
			"adding a validator with negative power results in error",
			[]abci.ValidatorUpdate{{PubKey: &pk2, Power: -100, ProTxHash: proTxHash2, NodeAddress: addr.String()}},
			defaultValidatorParams,
			true,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := sm.ValidateValidatorUpdates(tc.abciUpdates, tc.validatorParams)
			if tc.shouldErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestUpdateValidators(t *testing.T) {
	validatorSet, _ := types.RandValidatorSet(4)
	originalProTxHashes := validatorSet.GetProTxHashes()
	addedProTxHashes := crypto.RandProTxHashes(4)
	combinedProTxHashes := append(originalProTxHashes, addedProTxHashes...) //nolint:gocritic
	combinedValidatorSet, _ := types.GenerateValidatorSet(types.NewValSetParam(combinedProTxHashes))
	regeneratedValidatorSet, _ := types.GenerateValidatorSet(types.NewValSetParam(combinedProTxHashes))
	abciRegeneratedValidatorUpdates := regeneratedValidatorSet.ABCIEquivalentValidatorUpdates()
	removedProTxHashes := combinedValidatorSet.GetProTxHashes()[0 : len(combinedProTxHashes)-2] // these are sorted
	removedValidatorSet, _ := types.GenerateValidatorSet(types.NewValSetParam(
		removedProTxHashes,
	)) // size 6
	abciRemovalValidatorUpdates := removedValidatorSet.ABCIEquivalentValidatorUpdates()
	abciRemovalValidatorUpdates.ValidatorUpdates = append(
		abciRemovalValidatorUpdates.ValidatorUpdates,
		abciRegeneratedValidatorUpdates.ValidatorUpdates[6:]...)
	abciRemovalValidatorUpdates.ValidatorUpdates[6].Power = 0
	abciRemovalValidatorUpdates.ValidatorUpdates[7].Power = 0

	pubkeyRemoval := bls12381.GenPrivKey().PubKey()
	pk, err := encoding.PubKeyToProto(pubkeyRemoval)
	require.NoError(t, err)

	testCases := []struct {
		name string

		currentSet               *types.ValidatorSet
		abciUpdates              *abci.ValidatorSetUpdate
		thresholdPublicKeyUpdate crypto.PubKey

		resultingSet *types.ValidatorSet
		shouldErr    bool
	}{
		{
			"adding a validator set is OK",
			validatorSet,
			combinedValidatorSet.ABCIEquivalentValidatorUpdates(),
			combinedValidatorSet.ThresholdPublicKey,
			combinedValidatorSet,
			false,
		},
		{
			"updating a validator set is OK",
			combinedValidatorSet,
			regeneratedValidatorSet.ABCIEquivalentValidatorUpdates(),
			regeneratedValidatorSet.ThresholdPublicKey,
			regeneratedValidatorSet,
			false,
		},
		{
			"removing a validator is OK",
			regeneratedValidatorSet,
			abciRemovalValidatorUpdates,
			removedValidatorSet.ThresholdPublicKey,
			removedValidatorSet,
			false,
		},
		{
			"removing a non-existing validator results in error",
			removedValidatorSet,
			&abci.ValidatorSetUpdate{
				ValidatorUpdates: []abci.ValidatorUpdate{
					{ProTxHash: crypto.RandProTxHash(), Power: 0},
				},
				ThresholdPublicKey: pk,
				QuorumHash:         removedValidatorSet.QuorumHash,
			},
			removedValidatorSet.ThresholdPublicKey,
			removedValidatorSet,
			true,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			updates, _, _, err := types.PB2TM.ValidatorUpdatesFromValidatorSet(tc.abciUpdates)
			assert.NoError(t, err)
			err = tc.currentSet.UpdateWithChangeSet(
				updates,
				tc.thresholdPublicKeyUpdate,
				crypto.RandQuorumHash(),
			)
			if tc.shouldErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				require.Equal(t, tc.resultingSet.Size(), tc.currentSet.Size())

				assert.Equal(t, tc.resultingSet.TotalVotingPower(), tc.currentSet.TotalVotingPower())

				assert.Equal(t, tc.resultingSet.Validators[0].ProTxHash, tc.currentSet.Validators[0].ProTxHash)
				if tc.resultingSet.Size() > 1 {
					assert.Equal(t, tc.resultingSet.Validators[1].ProTxHash, tc.currentSet.Validators[1].ProTxHash)
				}
			}
		})
	}
}

// TestFinalizeBlockValidatorUpdates ensures we update validator set and send an event.
func TestFinalizeBlockValidatorUpdates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app := &testApp{}
	logger := log.NewNopLogger()
	cc := abciclient.NewLocalClient(logger, app)
	proxyApp := proxy.New(cc, logger, proxy.NopMetrics())
	err := proxyApp.Start(ctx)
	require.NoError(t, err)

	state, stateDB, _ := makeState(t, 1, 1)
	stateStore := sm.NewStore(stateDB)
	blockStore := store.NewBlockStore(dbm.NewMemDB())
	nodeProTxHash := state.Validators.Validators[0].ProTxHash
	ctx = dash.ContextWithProTxHash(ctx, nodeProTxHash)

	mp := &mpmocks.Mempool{}
	mp.On("Lock").Return()
	mp.On("Unlock").Return()
	mp.On("FlushAppConn", mock.Anything).Return(nil)
	mp.On("Update",
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything).Return(nil)
	mp.On("ReapMaxBytesMaxGas", mock.Anything, mock.Anything).Return(types.Txs{})

	eventBus := eventbus.NewDefault(logger)
	require.NoError(t, eventBus.Start(ctx))

	blockExec := sm.NewBlockExecutor(
		stateStore,
		proxyApp,
		mp,
		sm.EmptyEvidencePool{},
		blockStore,
		eventBus,
	)

	updatesSub, err := eventBus.SubscribeWithArgs(ctx, pubsub.SubscribeArgs{
		ClientID: "TestFinalizeBlockValidatorUpdates",
		Query:    types.EventQueryValidatorSetUpdates,
	})
	require.NoError(t, err)

	vals := state.Validators
	proTxHashes := vals.GetProTxHashes()

	addProTxHash := crypto.RandProTxHash()

	fmt.Printf("old: %x, new: %x\n", proTxHashes[0], addProTxHash)

	proTxHashes = append(proTxHashes, addProTxHash)
	newVals, _ := types.GenerateValidatorSet(types.NewValSetParam(proTxHashes))
	var pos int
	for i, proTxHash := range newVals.GetProTxHashes() {
		if bytes.Equal(proTxHash.Bytes(), addProTxHash.Bytes()) {
			pos = i
		}
	}

	// Ensure new validators have some node addresses set
	for _, validator := range newVals.Validators {
		if validator.NodeAddress.Zero() {
			validator.NodeAddress = types.RandValidatorAddress()
		}
	}

	app.ValidatorSetUpdate = newVals.ABCIEquivalentValidatorUpdates()

	const round = 0

	block, uncommittedState, err := blockExec.CreateProposalBlock(
		ctx,
		1,
		round,
		state,
		types.NewCommit(state.LastBlockHeight, 0, state.LastBlockID, nil, nil),
		proTxHashes[0],
		1,
	)
	require.NoError(t, err)
	blockID := block.BlockID(nil)
	require.NoError(t, err)
	state, err = blockExec.FinalizeBlock(ctx, state, uncommittedState, blockID, block, new(types.Commit))
	require.NoError(t, err)

	require.Nil(t, err)
	// test new validator was added to NextValidators
	if assert.Equal(t, state.LastValidators.Size()+1, state.Validators.Size()) {
		idx, _ := state.Validators.GetByProTxHash(addProTxHash)
		if idx < 0 {
			t.Fatalf("can't find proTxHash %v in the set %v", addProTxHash, state.Validators)
		}
	}

	// test we threw an event
	ctx, cancel = context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	msg, err := updatesSub.Next(ctx)
	require.NoError(t, err)
	event, ok := msg.Data().(types.EventDataValidatorSetUpdate)
	require.True(t, ok, "Expected event of type EventDataValidatorSetUpdate, got %T", msg.Data())
	assert.Len(t, event.QuorumHash, crypto.QuorumHashSize)
	if assert.NotEmpty(t, event.ValidatorSetUpdates) {
		assert.Equal(t, addProTxHash, event.ValidatorSetUpdates[pos].ProTxHash)
		assert.EqualValues(
			t,
			types.DefaultDashVotingPower,
			event.ValidatorSetUpdates[pos].VotingPower,
		)
	}
}

// TestFinalizeBlockValidatorUpdatesResultingInEmptySet checks that processing validator updates that
// would result in empty set causes no panic, an error is raised and NextValidators is not updated
func TestFinalizeBlockValidatorUpdatesResultingInEmptySet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app := &testApp{}
	logger := log.NewNopLogger()
	cc := abciclient.NewLocalClient(logger, app)
	proxyApp := proxy.New(cc, logger, proxy.NopMetrics())
	err := proxyApp.Start(ctx)
	require.NoError(t, err)

	eventBus := eventbus.NewDefault(logger)
	require.NoError(t, eventBus.Start(ctx))

	state, stateDB, _ := makeState(t, 1, 1)
	nodeProTxHash := state.Validators.Validators[0].ProTxHash
	ctx = dash.ContextWithProTxHash(ctx, nodeProTxHash)
	stateStore := sm.NewStore(stateDB)
	proTxHash := state.Validators.Validators[0].ProTxHash
	blockStore := store.NewBlockStore(dbm.NewMemDB())
	blockExec := sm.NewBlockExecutor(
		stateStore,
		proxyApp,
		new(mpmocks.Mempool),
		sm.EmptyEvidencePool{},
		blockStore,
		eventBus,
	)

	block, err := sf.MakeBlock(state, 1, new(types.Commit), 1)
	require.NoError(t, err)
	bps, err := block.MakePartSet(testPartSize)
	require.NoError(t, err)
	blockID := types.BlockID{Hash: block.Hash(), PartSetHeader: bps.Header()}

	publicKey, err := encoding.PubKeyToProto(bls12381.GenPrivKey().PubKey())
	require.NoError(t, err)
	// Remove the only validator
	validatorUpdates := []abci.ValidatorUpdate{
		{ProTxHash: proTxHash, Power: 0},
	}
	// the quorum hash needs to be the same
	// because we are providing an update removing a member from a known quorum, not changing the quorum
	app.ValidatorSetUpdate = &abci.ValidatorSetUpdate{
		ValidatorUpdates:   validatorUpdates,
		ThresholdPublicKey: publicKey,
		QuorumHash:         state.Validators.QuorumHash,
	}

	assert.NotPanics(t, func() {
		state, err = blockExec.ApplyBlock(ctx, state, blockID, block, new(types.Commit))
	})
	assert.NotNil(t, err)
	assert.NotEmpty(t, state.Validators.Validators)
}

func TestEmptyPrepareProposal(t *testing.T) {
	const height = 2
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := log.NewNopLogger()

	eventBus := eventbus.NewDefault(logger)
	require.NoError(t, eventBus.Start(ctx))
	state, stateDB, privVals := makeState(t, 1, height)
	app := abcimocks.NewApplication(t)

	reqPrepProposalMatch := mock.MatchedBy(func(req *abci.RequestPrepareProposal) bool {
		return len(req.QuorumHash) > 0 &&
			bytes.Equal(req.QuorumHash, state.Validators.QuorumHash) &&
			req.Height == height
	})

	app.On("PrepareProposal", mock.Anything, reqPrepProposalMatch).Return(&abci.ResponsePrepareProposal{
		AppHash: make([]byte, crypto.DefaultAppHashSize), AppVersion: 1,
	}, nil)
	cc := abciclient.NewLocalClient(logger, app)
	proxyApp := proxy.New(cc, logger, proxy.NopMetrics())
	err := proxyApp.Start(ctx)
	require.NoError(t, err)

	stateStore := sm.NewStore(stateDB)
	mp := &mpmocks.Mempool{}
	mp.On("Lock").Return()
	mp.On("Unlock").Return()
	mp.On("FlushAppConn", mock.Anything).Return(nil)
	mp.On("Update",
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything).Return(nil)
	mp.On("ReapMaxBytesMaxGas", mock.Anything, mock.Anything).Return(types.Txs{})

	blockExec := sm.NewBlockExecutor(
		stateStore,
		proxyApp,
		mp,
		sm.EmptyEvidencePool{},
		nil,
		eventBus,
	)
	proposer := state.Validators.GetByIndex(0)
	commit, _ := makeValidCommit(ctx, t, height, types.BlockID{}, state.Validators, privVals)
	_, _, err = blockExec.CreateProposalBlock(ctx, height, 0, state, commit, proposer.ProTxHash, 0)
	require.NoError(t, err)
}

// TestPrepareProposalErrorOnNonExistingRemoved tests that the block creation logic returns
// an error if the ResponsePrepareProposal returned from the application marks
//
//	a transaction as REMOVED that was not present in the original proposal.
func TestPrepareProposalErrorOnNonExistingRemoved(t *testing.T) {
	const height = 2
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := log.NewNopLogger()
	eventBus := eventbus.NewDefault(logger)
	require.NoError(t, eventBus.Start(ctx))

	state, stateDB, privVals := makeState(t, 1, height)
	stateStore := sm.NewStore(stateDB)

	evpool := &mocks.EvidencePool{}
	evpool.On("PendingEvidence", mock.Anything).Return([]types.Evidence{}, int64(0))

	mp := &mpmocks.Mempool{}
	mp.On("ReapMaxBytesMaxGas", mock.Anything, mock.Anything).Return(types.Txs{})

	app := abcimocks.NewApplication(t)

	// create an invalid ResponsePrepareProposal
	rpp := &abci.ResponsePrepareProposal{
		TxRecords: []*abci.TxRecord{
			{
				Action: abci.TxRecord_REMOVED,
				Tx:     []byte("new tx"),
			},
		},
		TxResults:  []*abci.ExecTxResult{{}},
		AppHash:    make([]byte, crypto.DefaultAppHashSize),
		AppVersion: 1,
	}
	app.On("PrepareProposal", mock.Anything, mock.Anything).Return(rpp, nil)

	cc := abciclient.NewLocalClient(logger, app)
	proxyApp := proxy.New(cc, logger, proxy.NopMetrics())
	err := proxyApp.Start(ctx)
	require.NoError(t, err)

	blockExec := sm.NewBlockExecutor(
		stateStore,
		proxyApp,
		mp,
		evpool,
		nil,
		eventBus,
	)
	proposer := state.Validators.GetByIndex(0)
	commit, _ := makeValidCommit(ctx, t, height, types.BlockID{}, state.Validators, privVals)
	block, _, err := blockExec.CreateProposalBlock(ctx, height, 0, state, commit, proposer.ProTxHash, 0)
	require.ErrorContains(t, err, "new transaction incorrectly marked as removed")
	require.Nil(t, block)

	mp.AssertExpectations(t)
}

// TestPrepareProposalRemoveTxs tests that any transactions marked as REMOVED
// are not included in the block produced by CreateProposalBlock. The test also
// ensures that any transactions removed are also removed from the mempool.
func TestPrepareProposalRemoveTxs(t *testing.T) {
	const height = 2
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := log.NewNopLogger()
	eventBus := eventbus.NewDefault(logger)
	require.NoError(t, eventBus.Start(ctx))

	state, stateDB, privVals := makeState(t, 1, height)
	stateStore := sm.NewStore(stateDB)

	evpool := &mocks.EvidencePool{}
	evpool.On("PendingEvidence", mock.Anything).Return([]types.Evidence{}, int64(0))

	txs := factory.MakeNTxs(height, 10)
	// 2 first transactions will be removed, so results only contain info about 8 txs
	txResults := factory.ExecTxResults(txs[2:])
	mp := &mpmocks.Mempool{}
	mp.On("ReapMaxBytesMaxGas", mock.Anything, mock.Anything).Return(txs)

	trs := txsToTxRecords(txs)
	trs[0].Action = abci.TxRecord_REMOVED
	trs[1].Action = abci.TxRecord_REMOVED
	mp.On("RemoveTxByKey", mock.Anything).Return(nil).Twice()

	app := abcimocks.NewApplication(t)
	app.On("PrepareProposal", mock.Anything, mock.Anything).Return(&abci.ResponsePrepareProposal{
		TxRecords:  trs,
		TxResults:  txResults,
		AppHash:    make([]byte, crypto.DefaultAppHashSize),
		AppVersion: 1,
	}, nil)

	cc := abciclient.NewLocalClient(logger, app)
	proxyApp := proxy.New(cc, logger, proxy.NopMetrics())
	err := proxyApp.Start(ctx)
	require.NoError(t, err)

	blockExec := sm.NewBlockExecutor(
		stateStore,
		proxyApp,
		mp,
		evpool,
		nil,
		eventBus,
	)
	val := state.Validators.GetByIndex(0)
	commit, _ := makeValidCommit(ctx, t, height, types.BlockID{}, state.Validators, privVals)
	block, _, err := blockExec.CreateProposalBlock(ctx, height, 0, state, commit, val.ProTxHash, 0)
	require.NoError(t, err)
	require.Len(t, block.Data.Txs.ToSliceOfBytes(), len(trs)-2)

	require.Equal(t, -1, block.Data.Txs.Index(types.Tx(trs[0].Tx)))
	require.Equal(t, -1, block.Data.Txs.Index(types.Tx(trs[1].Tx)))

	mp.AssertCalled(t, "RemoveTxByKey", types.Tx(trs[0].Tx).Key())
	mp.AssertCalled(t, "RemoveTxByKey", types.Tx(trs[1].Tx).Key())
	mp.AssertExpectations(t)
}

// TestPrepareProposalDelayedTxs tests that any transactions marked as DELAYED
// are not included in the block produced by CreateProposalBlock. The test also
// ensures that delayed transactions are NOT removed from the mempool.
func TestPrepareProposalDelayedTxs(t *testing.T) {
	const height = 2
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := log.NewNopLogger()
	eventBus := eventbus.NewDefault(logger)
	require.NoError(t, eventBus.Start(ctx))

	state, stateDB, privVals := makeState(t, 1, height)
	stateStore := sm.NewStore(stateDB)

	evpool := &mocks.EvidencePool{}
	evpool.On("PendingEvidence", mock.Anything).Return([]types.Evidence{}, int64(0))

	txs := factory.MakeNTxs(height, 10)
	// 2 first transactions will be removed, so results only contain info about 8 txs
	txResults := factory.ExecTxResults(txs[2:])
	mp := &mpmocks.Mempool{}
	mp.On("ReapMaxBytesMaxGas", mock.Anything, mock.Anything).Return(txs)

	trs := txsToTxRecords(txs)
	trs[0].Action = abci.TxRecord_DELAYED
	trs[1].Action = abci.TxRecord_DELAYED

	app := abcimocks.NewApplication(t)
	app.On("PrepareProposal", mock.Anything, mock.Anything).Return(&abci.ResponsePrepareProposal{
		TxRecords:  trs,
		TxResults:  txResults,
		AppHash:    make([]byte, crypto.DefaultAppHashSize),
		AppVersion: 1,
	}, nil)

	cc := abciclient.NewLocalClient(logger, app)
	proxyApp := proxy.New(cc, logger, proxy.NopMetrics())
	err := proxyApp.Start(ctx)
	require.NoError(t, err)

	blockExec := sm.NewBlockExecutor(
		stateStore,
		proxyApp,
		mp,
		evpool,
		nil,
		eventBus,
	)
	val := state.Validators.GetByIndex(0)
	commit, _ := makeValidCommit(ctx, t, height, types.BlockID{}, state.Validators, privVals)
	block, _, err := blockExec.CreateProposalBlock(ctx, height, 0, state, commit, val.ProTxHash, 0)
	require.NoError(t, err)
	require.Len(t, block.Data.Txs.ToSliceOfBytes(), len(trs)-2)

	require.Equal(t, -1, block.Data.Txs.Index(types.Tx(trs[0].Tx)))
	require.Equal(t, -1, block.Data.Txs.Index(types.Tx(trs[1].Tx)))

	mp.AssertExpectations(t)
}

// TestPrepareProposalAddedTxsIncluded tests that any transactions marked as ADDED
// in the prepare proposal response are included in the block.
func TestPrepareProposalAddedTxsIncluded(t *testing.T) {
	const height = 2
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := log.NewNopLogger()
	eventBus := eventbus.NewDefault(logger)
	require.NoError(t, eventBus.Start(ctx))

	state, stateDB, privVals := makeState(t, 1, height)
	stateStore := sm.NewStore(stateDB)

	evpool := &mocks.EvidencePool{}
	evpool.On("PendingEvidence", mock.Anything).Return([]types.Evidence{}, int64(0))

	txs := factory.MakeNTxs(height, 10)
	mp := &mpmocks.Mempool{}
	mp.On("ReapMaxBytesMaxGas", mock.Anything, mock.Anything).Return(txs[2:])

	trs := txsToTxRecords(txs)
	trs[0].Action = abci.TxRecord_ADDED
	trs[1].Action = abci.TxRecord_ADDED

	txres := factory.ExecTxResults(txs)

	app := abcimocks.NewApplication(t)
	app.On("PrepareProposal", mock.Anything, mock.Anything).Return(&abci.ResponsePrepareProposal{
		AppHash:    make([]byte, crypto.DefaultAppHashSize),
		TxRecords:  trs,
		TxResults:  txres,
		AppVersion: 1,
	}, nil)

	cc := abciclient.NewLocalClient(logger, app)
	proxyApp := proxy.New(cc, logger, proxy.NopMetrics())
	err := proxyApp.Start(ctx)
	require.NoError(t, err)

	blockExec := sm.NewBlockExecutor(
		stateStore,
		proxyApp,
		mp,
		evpool,
		nil,
		eventBus,
	)
	proposer := state.Validators.GetByIndex(0)
	commit, _ := makeValidCommit(ctx, t, height, types.BlockID{}, state.Validators, privVals)
	block, _, err := blockExec.CreateProposalBlock(ctx, height, 0, state, commit, proposer.ProTxHash, 0)
	require.NoError(t, err)

	require.Equal(t, txs[0], block.Data.Txs[0])
	require.Equal(t, txs[1], block.Data.Txs[1])

	mp.AssertExpectations(t)
}

// TestPrepareProposalReorderTxs tests that CreateBlock produces a block with transactions
// in the order matching the order they are returned from PrepareProposal.
func TestPrepareProposalReorderTxs(t *testing.T) {
	const height = 2
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := log.NewNopLogger()
	eventBus := eventbus.NewDefault(logger)
	require.NoError(t, eventBus.Start(ctx))

	state, stateDB, privVals := makeState(t, 1, height)
	stateStore := sm.NewStore(stateDB)

	evpool := &mocks.EvidencePool{}
	evpool.On("PendingEvidence", mock.Anything).Return([]types.Evidence{}, int64(0))

	txs := factory.MakeNTxs(height, 10)
	mp := &mpmocks.Mempool{}
	mp.On("ReapMaxBytesMaxGas", mock.Anything, mock.Anything).Return(txs)

	trs := txsToTxRecords(txs)
	trs = trs[2:]
	trs = append(trs[len(trs)/2:], trs[:len(trs)/2]...)

	txresults := factory.ExecTxResults(txs)
	txresults = txresults[2:]
	txresults = append(txresults[len(txresults)/2:], txresults[:len(txresults)/2]...)

	app := abcimocks.NewApplication(t)
	app.On("PrepareProposal", mock.Anything, mock.Anything).Return(&abci.ResponsePrepareProposal{
		AppHash:    make([]byte, crypto.DefaultAppHashSize),
		TxRecords:  trs,
		TxResults:  txresults,
		AppVersion: 1,
	}, nil)

	cc := abciclient.NewLocalClient(logger, app)
	proxyApp := proxy.New(cc, logger, proxy.NopMetrics())
	err := proxyApp.Start(ctx)
	require.NoError(t, err)

	blockExec := sm.NewBlockExecutor(
		stateStore,
		proxyApp,
		mp,
		evpool,
		nil,
		eventBus,
	)
	proposer := state.Validators.GetByIndex(0)
	commit, _ := makeValidCommit(ctx, t, height, types.BlockID{}, state.Validators, privVals)
	block, _, err := blockExec.CreateProposalBlock(ctx, height, 0, state, commit, proposer.ProTxHash, 0)
	require.NoError(t, err)
	for i, tx := range block.Data.Txs {
		require.Equal(t, types.Tx(trs[i].Tx), tx)
	}

	mp.AssertExpectations(t)

}

// TestPrepareProposalErrorOnTooManyTxs tests that the block creation logic returns
// an error if the ResponsePrepareProposal returned from the application is invalid.
func TestPrepareProposalErrorOnTooManyTxs(t *testing.T) {
	const (
		height           = 2
		bytesPerTx int64 = 3
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := log.NewNopLogger()
	eventBus := eventbus.NewDefault(logger)
	require.NoError(t, eventBus.Start(ctx))

	state, stateDB, privVals := makeState(t, 1, height)
	// limit max block size
	state.ConsensusParams.Block.MaxBytes = 60 * 1024
	stateStore := sm.NewStore(stateDB)

	evpool := &mocks.EvidencePool{}
	evpool.On("PendingEvidence", mock.Anything).Return([]types.Evidence{}, int64(0))

	commit, _ := makeValidCommit(ctx, t, height, types.BlockID{}, state.Validators, privVals)

	maxDataBytes, err := types.MaxDataBytes(state.ConsensusParams.Block.MaxBytes, commit, 0)
	require.NoError(t, err)
	txs := factory.MakeNTxs(height, maxDataBytes/bytesPerTx+2) // +2 so that tx don't fit
	mp := &mpmocks.Mempool{}
	mp.On("ReapMaxBytesMaxGas", mock.Anything, mock.Anything).Return(txs)

	trs := txsToTxRecords(txs)

	app := abcimocks.NewApplication(t)
	app.On("PrepareProposal", mock.Anything, mock.Anything).Return(&abci.ResponsePrepareProposal{
		TxRecords:  trs,
		TxResults:  factory.ExecTxResults(txs),
		AppHash:    make([]byte, crypto.DefaultAppHashSize),
		AppVersion: 1,
	}, nil)

	cc := abciclient.NewLocalClient(logger, app)
	proxyApp := proxy.New(cc, logger, proxy.NopMetrics())
	err = proxyApp.Start(ctx)
	require.NoError(t, err)

	blockExec := sm.NewBlockExecutor(
		stateStore,
		proxyApp,
		mp,
		evpool,
		nil,
		eventBus,
	)
	proposer := state.Validators.GetByIndex(0)

	block, _, err := blockExec.CreateProposalBlock(ctx, height, 0, state, commit, proposer.ProTxHash, 0)
	require.ErrorContains(t, err, "transaction data size exceeds maximum")
	require.Nil(t, block, "")

	mp.AssertExpectations(t)
}

// TestPrepareProposalErrorOnPrepareProposalError tests when the client returns an error
// upon calling PrepareProposal on it.
func TestPrepareProposalErrorOnPrepareProposalError(t *testing.T) {
	const height = 2
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := log.NewNopLogger()
	eventBus := eventbus.NewDefault(logger)
	require.NoError(t, eventBus.Start(ctx))

	state, stateDB, privVals := makeState(t, 1, height)
	stateStore := sm.NewStore(stateDB)

	evpool := &mocks.EvidencePool{}
	evpool.On("PendingEvidence", mock.Anything).Return([]types.Evidence{}, int64(0))

	txs := factory.MakeNTxs(height, 10)
	mp := &mpmocks.Mempool{}
	mp.On("ReapMaxBytesMaxGas", mock.Anything, mock.Anything).Return(txs)

	cm := &abciclientmocks.Client{}
	cm.On("IsRunning").Return(true)
	cm.On("Error").Return(nil)
	cm.On("Start", mock.Anything).Return(nil).Once()
	cm.On("Wait").Return(nil).Once()
	cm.On("PrepareProposal", mock.Anything, mock.Anything).Return(nil, errors.New("an injected error")).Once()

	proxyApp := proxy.New(cm, logger, proxy.NopMetrics())
	err := proxyApp.Start(ctx)
	require.NoError(t, err)

	blockExec := sm.NewBlockExecutor(
		stateStore,
		proxyApp,
		mp,
		evpool,
		nil,
		eventBus,
	)
	val := state.Validators.GetByIndex(0)
	commit, _ := makeValidCommit(ctx, t, height, types.BlockID{}, state.Validators, privVals)

	block, _, err := blockExec.CreateProposalBlock(ctx, height, 0, state, commit, val.ProTxHash, 0)
	require.Nil(t, block)
	require.ErrorContains(t, err, "an injected error")

	mp.AssertExpectations(t)
}

func txsToTxRecords(txs []types.Tx) []*abci.TxRecord {
	trs := make([]*abci.TxRecord, len(txs))
	for i, tx := range txs {
		trs[i] = &abci.TxRecord{
			Action: abci.TxRecord_UNMODIFIED,
			Tx:     tx,
		}
	}
	return trs
}
