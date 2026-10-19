package consensus

import (
	"testing"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/abci/types/mocks"
	"github.com/dashpay/tenderdash/crypto"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/store"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

func TestInitChainChangesEvidenceMode(t *testing.T) {
	for _, tc := range []struct {
		name          string
		before, after int64
	}{
		{"enable", 0, 1000}, {"disable", 1000, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := types.DefaultConsensusParams()
			params.Evidence.MaxBytes = tc.before
			genesis, _ := factory.RandGenesisDoc(1, params)
			state, err := sm.MakeGenesisState(genesis)
			require.NoError(t, err)
			updates := params.ToProto()
			updates.Evidence.MaxBytes = tc.after
			app := mocks.NewApplication(t)
			app.On("InitChain", mock.Anything, mock.Anything).Return(&abci.ResponseInitChain{
				AppHash: make([]byte, crypto.DefaultAppHashSize), ConsensusParams: &updates,
			}, nil).Once()
			stateStore := sm.NewStore(dbm.NewMemDB())
			blockStore := store.NewBlockStore(dbm.NewMemDB())
			client := abciclient.NewLocalClient(log.NewNopLogger(), app)
			replayer := newBlockReplayer(stateStore, blockStore, genesis, nil, client, nil)
			_, err = replayer.Replay(t.Context(), state, genesis.AppHash, 0)
			require.NoError(t, err)
			stored, err := stateStore.Load()
			require.NoError(t, err)
			require.Equal(t, tc.after, stored.ConsensusParams.Evidence.MaxBytes)
		})
	}
}
