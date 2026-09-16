package store

import (
	"testing"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/config"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/state/test/factory"
	"github.com/dashpay/tenderdash/internal/test/dbspy"
	"github.com/dashpay/tenderdash/types"
)

func saveOneBlock(t *testing.T, bs *BlockStore) *types.Block {
	t.Helper()
	cfg, err := config.ResetTestRoot(t.TempDir(), "nofsync_test")
	require.NoError(t, err)
	state, err := sm.MakeGenesisStateFromFile(cfg.GenesisFile())
	require.NoError(t, err)
	block, err := factory.MakeBlock(state, 1, new(types.Commit), 0)
	require.NoError(t, err)
	parts, err := block.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)
	bs.SaveBlock(block, parts, makeTestCommit(t, state, 1))
	return block
}

// TestBlockStoreWritesAreDurableByDefault pins the default: a store built
// without options commits every batch with WriteSync.
func TestBlockStoreWritesAreDurableByDefault(t *testing.T) {
	db := dbspy.New(dbm.NewMemDB())
	bs := NewBlockStore(db)

	block := saveOneBlock(t, bs)

	require.Equal(t, 1, db.SyncWrites, "SaveBlock must fsync by default")
	require.Zero(t, db.Writes)
	require.False(t, bs.UnsafeNoFsync())
	require.Equal(t, block.Hash(), bs.LoadBlock(1).Hash())
}

// TestBlockStoreUnsafeNoFsyncSkipsSync checks the benchmark switch selects the
// non-syncing write and stores exactly the same data, and that it is a
// property of the one store it was passed to, not of the process.
func TestBlockStoreUnsafeNoFsyncSkipsSync(t *testing.T) {
	db := dbspy.New(dbm.NewMemDB())
	bs := NewBlockStore(db, WithUnsafeNoFsync())

	block := saveOneBlock(t, bs)

	require.True(t, bs.UnsafeNoFsync())
	require.Equal(t, 1, db.Writes, "SaveBlock must not fsync with WithUnsafeNoFsync")
	require.Zero(t, db.SyncWrites)
	require.Equal(t, block.Hash(), bs.LoadBlock(1).Hash())

	// deletions go through the same policy
	_, err := bs.DeleteBlock(1)
	require.NoError(t, err)
	require.Equal(t, 2, db.Writes)
	require.Zero(t, db.SyncWrites)

	// another store over another DB is unaffected
	other := dbspy.New(dbm.NewMemDB())
	saveOneBlock(t, NewBlockStore(other))
	require.Equal(t, 1, other.SyncWrites)
	require.Zero(t, other.Writes)
}
