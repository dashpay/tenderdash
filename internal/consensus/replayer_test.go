package consensus

import (
	"context"
	"encoding/hex"
	"fmt"

	"testing"

	"github.com/stretchr/testify/require"
	dbm "github.com/tendermint/tm-db"

	abciclient "github.com/tendermint/tendermint/abci/client"
	"github.com/tendermint/tendermint/abci/example/kvstore"
	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/internal/eventbus"
	"github.com/tendermint/tendermint/internal/proxy"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/internal/store"
	"github.com/tendermint/tendermint/libs/log"
	tmtypes "github.com/tendermint/tendermint/types"
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

	lastAppHash := mustHexToBytes("434BB5713255371561623E144D06F3056A65FD66AF40207FBA4451DA5A6A4025")

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

func updateStateStoreWithState(state sm.State, store sm.Store) sm.Store {
	err := store.Save(state)
	if err != nil {
		panic(err)
	}
	return store
}

func mustHexToBytes(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}
