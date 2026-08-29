package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	clientmocks "github.com/dashpay/tenderdash/abci/client/mocks"
	"github.com/dashpay/tenderdash/internal/state/mocks"
)

// fakeStateSyncMetricer is a statesync.Metricer stub with fixed values.
type fakeStateSyncMetricer struct{}

func (fakeStateSyncMetricer) TotalSnapshots() int64              { return 2 }
func (fakeStateSyncMetricer) ChunkProcessAvgTime() time.Duration { return 5 * time.Second }
func (fakeStateSyncMetricer) SnapshotHeight() int64              { return 7 }
func (fakeStateSyncMetricer) SnapshotChunksCount() int64         { return 3 }

// SnapshotChunksTotal only satisfies the interface: SyncInfo has no
// snapshot_chunks_total field, so /status never reads it.
func (fakeStateSyncMetricer) SnapshotChunksTotal() int64 { return 4 }
func (fakeStateSyncMetricer) BackFilledBlocks() int64    { return 5 }
func (fakeStateSyncMetricer) BackFillBlocksTotal() int64 { return 6 }

// TestStatusStateSyncMetrics verifies that /status copies the state-sync
// metrics from the wired Metricer into sync_info.
func TestStatusStateSyncMetrics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	blockStore := mocks.NewBlockStore(t)
	blockStore.On("LoadBaseMeta").Return(nil)
	blockStore.On("Height").Return(int64(0))

	proxyApp := clientmocks.NewClient(t)
	proxyApp.On("Info", mock.Anything, mock.Anything).Return(nil, errors.New("no app"))

	env := &Environment{
		BlockStore:        blockStore,
		ProxyApp:          proxyApp,
		StateSyncMetricer: fakeStateSyncMetricer{},
	}

	status, err := env.Status(ctx)
	require.NoError(t, err)

	require.Equal(t, int64(2), status.SyncInfo.TotalSnapshots)
	require.Equal(t, 5*time.Second, status.SyncInfo.ChunkProcessAvgTime)
	require.Equal(t, int64(7), status.SyncInfo.SnapshotHeight)
	require.Equal(t, int64(3), status.SyncInfo.SnapshotChunksCount)
	require.Equal(t, int64(5), status.SyncInfo.BackFilledBlocks)
	require.Equal(t, int64(6), status.SyncInfo.BackFillBlocksTotal)
}
