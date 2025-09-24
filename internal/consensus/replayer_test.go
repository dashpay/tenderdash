package consensus

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/dashd-go/btcjson"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	"github.com/dashpay/tenderdash/abci/example/kvstore"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/abci/types/mocks"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	"github.com/dashpay/tenderdash/internal/eventbus"
	"github.com/dashpay/tenderdash/internal/proxy"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/store"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
	tmtypes "github.com/dashpay/tenderdash/types"
)

func TestBlockReplayerReplay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const (
		chainSize     = 6
		nVals         = 1
		lastHeightIDx = chainSize - 1
	)

	logger := log.NewNopLogger()

	eventBus := eventbus.NewDefault(log.NewNopLogger())
	require.NoError(t, eventBus.Start(ctx))

	gen := NewChainGenerator(t, nVals, chainSize)
	chain := gen.Generate(ctx, t)

	lastAppHash := tmbytes.MustHexDecode("434BB5713255371561623E144D06F3056A65FD66AF40207FBA4451DA5A6A4025")

	testCases := []struct {
		state       sm.State
		stateStore  sm.Store
		blockStore  sm.BlockStore
		appAppHash  []byte
		appHeight   int64
		wantAppHash []byte
		wantNBlocks int
		appOpts     []kvstore.OptFunc
	}{
		{
			// state-height is equal to block-height and app-height is less block-height
			// state-height = H, block-height == H, app-height == 0
			state:       chain.States[lastHeightIDx],
			stateStore:  chain.StateStore,
			blockStore:  chain.BlockStore,
			appHeight:   0,
			appAppHash:  make([]byte, crypto.DefaultAppHashSize),
			wantAppHash: lastAppHash,
			wantNBlocks: chainSize, // sync the whole chain
		},
		{
			// state-height is equal to block-height and app-height is equal to block-height
			// state-height == H, block-height == H, app-height == H
			state:       chain.States[lastHeightIDx],
			stateStore:  chain.StateStore,
			blockStore:  chain.BlockStore,
			appHeight:   chainSize,
			appAppHash:  lastAppHash,
			wantAppHash: lastAppHash,
			wantNBlocks: 0, // the chain is already synced
		},
		{
			// state-height is one behind of block-height and app-height is less state-height
			// state-height == H-1, block-height == H, app-height == 0
			state:       chain.States[lastHeightIDx-1],
			stateStore:  updateStateStoreWithState(chain.States[lastHeightIDx-1], chain.StateStore),
			blockStore:  chain.BlockStore,
			appHeight:   0,
			appAppHash:  make([]byte, crypto.DefaultAppHashSize),
			wantAppHash: lastAppHash,
			wantNBlocks: chainSize, // sync the whole chain
		},
		{
			// state-height is one behind of block-height and app-height is equal to state-height
			// state-height == H-1, block-height == H, app-height == H-1
			state:       chain.States[lastHeightIDx-1],
			stateStore:  updateStateStoreWithState(chain.States[lastHeightIDx-1], chain.StateStore),
			blockStore:  chain.BlockStore,
			appHeight:   chainSize - 1,
			appAppHash:  chain.States[lastHeightIDx-1].LastAppHash,
			wantAppHash: lastAppHash,
			wantNBlocks: 1, // sync only last block
			appOpts: []kvstore.OptFunc{
				kvstore.WithState(chainSize-1, chain.States[lastHeightIDx-1].LastAppHash),
				kvstore.WithValidatorSetUpdates(map[int64]abci.ValidatorSetUpdate{
					0: tmtypes.TM2PB.ValidatorUpdates(chain.GenesisState.Validators),
				}),
			},
		},
		{
			// state-height is one behind of block-height and app-height is equal to block-height
			// state-height == H-1, block-height == H, app-height == H
			state:       chain.States[lastHeightIDx-1],
			stateStore:  updateStateStoreWithState(chain.States[lastHeightIDx-1], chain.StateStore),
			blockStore:  chain.BlockStore,
			appHeight:   chainSize,
			appAppHash:  chain.States[lastHeightIDx].LastAppHash,
			wantAppHash: lastAppHash,
			wantNBlocks: 1,
		},
		{
			// tenderdash state at the initial height, replayer does InitChain call to get initial-state form the app
			// state-height == 0, block-height == 0, app-height == 0
			state:       chain.GenesisState,
			stateStore:  sm.NewStore(dbm.NewMemDB()),
			blockStore:  store.NewBlockStore(dbm.NewMemDB()),
			appHeight:   0,
			appAppHash:  make([]byte, crypto.DefaultAppHashSize),
			wantAppHash: make([]byte, crypto.DefaultAppHashSize),
			wantNBlocks: 0,
		},
	}

	for i, tc := range testCases {
		t.Run(fmt.Sprintf("test-case-%d", i+1), func(t *testing.T) {
			app, err := kvstore.NewMemoryApp(tc.appOpts...)
			require.NoError(t, err)
			client := abciclient.NewLocalClient(logger, app)
			proxyApp := proxy.New(client, logger, proxy.NopMetrics())
			replayer := newBlockReplayer(tc.stateStore, tc.blockStore, chain.GenesisDoc, eventBus, proxyApp, chain.ProTxHash)
			appHash, err := replayer.Replay(ctx, tc.state, tc.appAppHash, tc.appHeight)
			require.NoError(t, err)
			require.Equal(t, tc.wantAppHash, appHash)
			require.Equal(t, tc.wantNBlocks, replayer.nBlocks)
		})
	}
}

// TestInitChainGenesisTime checks if genesis time provided to InitChain is correctly included in first block.
//
// Given some hardcoded genesis time,
// When I return it in response to InitChain,
// Then the first block should have that genesis time.
func TestInitChainGenesisTime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := log.NewTestingLogger(t)

	eventBus := eventbus.NewDefault(logger)
	require.NoError(t, eventBus.Start(ctx))

	genesisAppHash := make([]byte, crypto.DefaultAppHashSize)

	genesisTime := time.Date(2001, 12, 31, 12, 34, 56, 78, time.UTC)

	// configure mock app to return expected genesis time
	app := mocks.NewApplication(t)
	defer app.AssertExpectations(t)
	app.On("InitChain", mock.Anything, mock.Anything).Return(&abci.ResponseInitChain{
		AppHash: genesisAppHash,
		XGenesisTime: &abci.ResponseInitChain_GenesisTime{
			GenesisTime: &genesisTime,
		},
	}, nil)

	client := abciclient.NewLocalClient(logger, app)
	proxyApp := proxy.New(client, logger, proxy.NopMetrics())

	// Prepare genesis document with another genesis time
	vset, _ := factory.MockValidatorSet()
	recoveredThresholdPublicKey, err := bls12381.RecoverThresholdPublicKeyFromPublicKeys(
		vset.GetPublicKeys(),
		vset.GetProTxHashesAsByteArrays(),
	)
	require.NoError(t, err)
	vset.ThresholdPublicKey = recoveredThresholdPublicKey

	genDoc := tmtypes.GenesisDoc{
		ChainID:            "test-chain",
		QuorumType:         btcjson.LLMQType_100_67,
		Validators:         tmtypes.MakeGenesisValsFromValidatorSet(vset),
		ConsensusParams:    tmtypes.DefaultConsensusParams(),
		ThresholdPublicKey: recoveredThresholdPublicKey,
		QuorumHash:         make([]byte, 32),
		GenesisTime:        time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	// We must synchronize genesis validator set voting threshold with the one from the validator set.
	// In normal cicrumstances, the threshold is set in genesis or in ResponseInitChain.
	genDoc.ConsensusParams.Validator.VotingPowerThreshold = &vset.VotingPowerThreshold

	err = genDoc.ValidateAndComplete()
	require.NoError(t, err)

	// initialize state and stores
	smState, err := sm.MakeGenesisState(&genDoc)
	require.NoError(t, err)
	stateStore := sm.NewStore(dbm.NewMemDB())
	blockStore := store.NewBlockStore(dbm.NewMemDB())
	proposer := smState.GetProposerFromState(1, 0)
	proposerProTxHash := proposer.ProTxHash
	replayer := newBlockReplayer(stateStore, blockStore, &genDoc, eventBus, proxyApp, proposerProTxHash)

	// use replayer to call initChain
	appHash, err := replayer.Replay(ctx, smState, bytes.Clone(genesisAppHash), 0)
	require.NoError(t, err)
	require.NotEmpty(t, appHash)

	// reload smState, as replayer does not modify it
	smState, err = stateStore.Load()
	require.NoError(t, err)

	// ensure the block contains expected genesis time
	block := smState.MakeBlock(1, []tmtypes.Tx{}, nil, nil, proposerProTxHash, 1)
	assert.Equal(t, genesisTime, block.Header.Time, "block: %+v\n\nsm.State: %+v", block, smState)

}

func updateStateStoreWithState(state sm.State, store sm.Store) sm.Store {
	err := store.Save(state)
	if err != nil {
		panic(err)
	}
	return store
}
