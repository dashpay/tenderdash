package consensus

import (
	"context"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	abciclient "github.com/tendermint/tendermint/abci/client"
	"github.com/tendermint/tendermint/abci/example/kvstore"
	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/internal/eventbus"
	"github.com/tendermint/tendermint/internal/proxy"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/libs/log"
	tmtypes "github.com/tendermint/tendermint/types"
)

func TestBlockReplayerReplay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const (
		chainLen = 6
		nVals    = 1
	)

	logger := log.NewNopLogger()

	eventBus := eventbus.NewDefault(log.NewNopLogger())
	require.NoError(t, eventBus.Start(ctx))

	gen := NewChainGenerator(t, nVals, chainLen)
	chain := gen.Generate(ctx)

	lastAppHash := mustHexToBytes("434BB5713255371561623E144D06F3056A65FD66AF40207FBA4451DA5A6A4025")

	testCases := []struct {
		state       sm.State
		stateStore  sm.Store
		blockStore  sm.BlockStore
		appAppHash  []byte
		appHeight   int64
		wantAppHash []byte
		wantNBlocks int
		appOpts     []func(app *kvstore.Application)
	}{
		{
			// state-height is equal to store-height and app-height is less store-height
			state:       chain.States[len(chain.States)-1],
			stateStore:  chain.StateStore,
			blockStore:  chain.BlockStore,
			appHeight:   0,
			appAppHash:  make([]byte, crypto.DefaultAppHashSize),
			wantAppHash: lastAppHash,
			wantNBlocks: chainLen,
		},
		{
			// state-height is equal to store-height and app-height is equal to store-height
			state:       chain.States[chainLen-1],
			stateStore:  chain.StateStore,
			blockStore:  chain.BlockStore,
			appHeight:   chainLen,
			appAppHash:  lastAppHash,
			wantAppHash: lastAppHash,
			wantNBlocks: 0,
		},
		{
			// state-height is one ahead of store-height and app-height is less state-height
			state:       chain.States[chainLen-2],
			stateStore:  updateStateStoreWithState(chain.States[len(chain.States)-2], chain.StateStore),
			blockStore:  chain.BlockStore,
			appHeight:   0,
			appAppHash:  make([]byte, crypto.DefaultAppHashSize),
			wantAppHash: lastAppHash,
			wantNBlocks: chainLen,
		},
		{
			// state-height is one ahead of store-height and app-height is equal to state-height
			state:       chain.States[len(chain.States)-2],
			stateStore:  updateStateStoreWithState(chain.States[len(chain.States)-2], chain.StateStore),
			blockStore:  chain.BlockStore,
			appHeight:   chainLen - 1,
			appAppHash:  chain.States[chainLen-2].AppHash,
			wantAppHash: lastAppHash,
			wantNBlocks: 1,
			appOpts: []func(application *kvstore.Application){
				kvstore.WithState(chainLen-1, chain.States[chainLen-2].AppHash),
				kvstore.WithValidatorSetUpdates(map[int64]abci.ValidatorSetUpdate{
					0: tmtypes.TM2PB.ValidatorUpdates(chain.GenesisState.Validators),
				}),
			},
		},
		{
			// state-height is one ahead of store-height and app-height is equal to store-height
			state:       chain.States[len(chain.States)-2],
			stateStore:  updateStateStoreWithState(chain.States[len(chain.States)-2], chain.StateStore),
			blockStore:  chain.BlockStore,
			appHeight:   chainLen,
			appAppHash:  chain.States[chainLen-1].AppHash,
			wantAppHash: lastAppHash,
			wantNBlocks: 1,
		},
	}

	for i, tc := range testCases {
		t.Run(fmt.Sprintf("text-case #%d", i+1), func(t *testing.T) {
			client := abciclient.NewLocalClient(logger, kvstore.NewApplication(tc.appOpts...))
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
