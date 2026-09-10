package state_test

import (
	"testing"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/require"

	abci "github.com/dashpay/tenderdash/abci/types"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/test/dbspy"
	tmstate "github.com/dashpay/tenderdash/proto/tendermint/state"
)

func saveStateAndResponses(t *testing.T, store sm.Store) sm.State {
	t.Helper()
	state, _, _ := makeState(t, 1, 1)
	require.NoError(t, store.Save(state))
	require.NoError(t, store.SaveABCIResponses(1, tmstate.ABCIResponses{
		ProcessProposal: &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_ACCEPT},
	}))
	return state
}

// TestStateStoreWritesAreDurableByDefault pins the default: without options
// the state and the ABCI responses are written with the syncing variants.
func TestStateStoreWritesAreDurableByDefault(t *testing.T) {
	db := dbspy.New(dbm.NewMemDB())
	store := sm.NewStore(db)

	state := saveStateAndResponses(t, store)

	require.Equal(t, 1, db.SyncWrites, "Save must fsync by default")
	require.Equal(t, 1, db.SyncSets, "SaveABCIResponses must fsync by default")
	require.Zero(t, db.Writes)
	require.Zero(t, db.Sets)

	loaded, err := store.Load()
	require.NoError(t, err)
	require.Equal(t, state.LastBlockHeight, loaded.LastBlockHeight)
}

// TestStateStoreUnsafeNoFsyncSkipsSync checks the benchmark switch selects the
// non-syncing writes for both the batch and the single-key paths, stores the
// same data, and applies only to the store it was passed to.
func TestStateStoreUnsafeNoFsyncSkipsSync(t *testing.T) {
	db := dbspy.New(dbm.NewMemDB())
	store := sm.NewStore(db, sm.StoreWithUnsafeNoFsync())

	state := saveStateAndResponses(t, store)

	require.Equal(t, 1, db.Writes, "Save must not fsync with StoreWithUnsafeNoFsync")
	require.Equal(t, 1, db.Sets, "SaveABCIResponses must not fsync with StoreWithUnsafeNoFsync")
	require.Zero(t, db.SyncWrites)
	require.Zero(t, db.SyncSets)

	loaded, err := store.Load()
	require.NoError(t, err)
	require.Equal(t, state.LastBlockHeight, loaded.LastBlockHeight)
	resp, err := store.LoadABCIResponses(1)
	require.NoError(t, err)
	require.Equal(t, abci.ResponseProcessProposal_ACCEPT, resp.ProcessProposal.Status)

	// another store over another DB keeps the default
	other := dbspy.New(dbm.NewMemDB())
	saveStateAndResponses(t, sm.NewStore(other))
	require.Equal(t, 1, other.SyncWrites)
	require.Equal(t, 1, other.SyncSets)
	require.Zero(t, other.Writes)
	require.Zero(t, other.Sets)
}

// TestNewStoreNilLoggerIsIgnored keeps the old contract that a nil logger
// falls back to the nop logger rather than a nil dereference on first use.
func TestNewStoreNilLoggerIsIgnored(t *testing.T) {
	store := sm.NewStore(dbm.NewMemDB(), sm.StoreWithLogger(nil))
	require.NotPanics(t, func() { _ = store.PruneStates(1) })
}
