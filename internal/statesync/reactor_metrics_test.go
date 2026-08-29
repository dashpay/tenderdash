package statesync

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

// TestReactorMetricer exercises the full Metricer surface across the three
// phases of a sync's lifetime: before any sync, while a sync is in progress
// (values read from the live syncer), and after the sync ends (values
// snapshotted before the syncer is dropped).
func TestReactorMetricer(t *testing.T) {
	r := &Reactor{logger: log.NewTestingLogger(t)}
	var m Metricer = r

	// no sync has ever run: everything reports zero
	require.Zero(t, m.TotalSnapshots())
	require.Zero(t, m.ChunkProcessAvgTime())
	require.Zero(t, m.SnapshotHeight())
	require.Zero(t, m.SnapshotChunksCount())
	require.Zero(t, m.SnapshotChunksTotal())
	require.Zero(t, m.BackFilledBlocks())
	require.Zero(t, m.BackFillBlocksTotal())

	// build a live syncer with two discovered snapshots and a chunk queue
	// holding three known chunks, one of which has been processed
	snap := &snapshot{Height: 7, Version: 1, Hash: []byte{0x01}}
	pool := newSnapshotPool()
	added, err := pool.Add(types.NodeID("aa"), snap)
	require.NoError(t, err)
	require.True(t, added)
	added, err = pool.Add(types.NodeID("aa"), &snapshot{Height: 8, Version: 1, Hash: []byte{0x02}})
	require.NoError(t, err)
	require.True(t, added)

	queue, err := newChunkQueue(snap, t.TempDir(), 4)
	require.NoError(t, err)
	defer func() { require.NoError(t, queue.Close()) }()

	chunkIDs := [][]byte{{0x01}, {0x02}, {0x03}}
	queue.Enqueue(chunkIDs...)
	dequeued, err := queue.Dequeue()
	require.NoError(t, err)
	require.Equal(t, chunkIDs[0], []byte(dequeued))
	added, err = queue.Add(&chunk{Height: 7, Version: 1, ID: chunkIDs[0], Chunk: []byte("data")})
	require.NoError(t, err)
	require.True(t, added)
	_, err = queue.Next()
	require.NoError(t, err)

	s := &syncer{
		logger:                   log.NewTestingLogger(t),
		snapshots:                pool,
		chunkQueue:               queue,
		avgChunkTime:             int64(5 * time.Second),
		lastSyncedSnapshotHeight: 7,
	}
	r.syncer = s

	// during the sync: values come from the live syncer
	require.Equal(t, int64(2), m.TotalSnapshots())
	require.Equal(t, 5*time.Second, m.ChunkProcessAvgTime())
	require.Equal(t, int64(7), m.SnapshotHeight())
	require.Equal(t, int64(1), m.SnapshotChunksCount())
	require.Equal(t, int64(3), m.SnapshotChunksTotal())

	// the syncer records the queue's final counts when dropping it
	s.releaseChunkQueue()
	require.Nil(t, s.chunkQueue)
	require.Equal(t, int64(1), m.SnapshotChunksCount())
	require.Equal(t, int64(3), m.SnapshotChunksTotal())

	// syncComplete drops the syncer but snapshots its final values first
	r.syncComplete()
	require.Nil(t, r.syncer)
	require.Equal(t, int64(2), m.TotalSnapshots())
	require.Equal(t, 5*time.Second, m.ChunkProcessAvgTime())
	require.Equal(t, int64(7), m.SnapshotHeight())
	require.Equal(t, int64(1), m.SnapshotChunksCount())
	require.Equal(t, int64(3), m.SnapshotChunksTotal())
}
