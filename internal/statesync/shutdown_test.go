package statesync

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	clientmocks "github.com/dashpay/tenderdash/abci/client/mocks"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/internal/p2p"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/statesync/mocks"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
	ssproto "github.com/dashpay/tenderdash/proto/tendermint/statesync"
	"github.com/dashpay/tenderdash/types"
)

func TestSyncFullQueueTeardown(t *testing.T) {
	for _, result := range []abci.ResponseApplySnapshotChunk_Result{
		abci.ResponseApplySnapshotChunk_ABORT,
		abci.ResponseApplySnapshotChunk_REJECT_SNAPSHOT,
		abci.ResponseApplySnapshotChunk_RETRY_SNAPSHOT,
	} {
		t.Run(result.String(), func(t *testing.T) {
			snap := &snapshot{Height: 3, Version: 1, Hash: []byte{1}}
			q, err := newChunkQueue(snap, t.TempDir(), 4)
			require.NoError(t, err)
			defer func() { require.NoError(t, q.Close()) }()
			provider := mocks.NewStateProvider(t)
			provider.On("AppHash", mock.Anything, uint64(3)).Return(tmbytes.HexBytes{1}, nil)
			provider.On("State", mock.Anything, uint64(3)).Return(sm.State{}, nil)
			provider.On("Commit", mock.Anything, uint64(3)).Return(&types.Commit{}, nil)
			conn := clientmocks.NewClient(t)
			conn.On("OfferSnapshot", mock.Anything, mock.Anything).Return(&abci.ResponseOfferSnapshot{
				Result: abci.ResponseOfferSnapshot_ACCEPT,
			}, nil)
			s := &syncer{logger: log.NewNopLogger(), stateProvider: provider, conn: conn}
			r := &Reactor{logger: log.NewNopLogger(), syncer: s}
			attempts := 1
			if result == abci.ResponseApplySnapshotChunk_RETRY_SNAPSHOT {
				attempts = 2
			}
			for attempt := 0; attempt < attempts; attempt++ {
				entered, finish := make(chan struct{}), make(chan struct{})
				conn.On("ApplySnapshotChunk", mock.Anything, mock.Anything).Run(func(_ mock.Arguments) {
					close(entered)
					<-finish
				}).Return(&abci.ResponseApplySnapshotChunk{Result: result}, nil).Once()
				for id := byte(1); id <= 6; id++ {
					if attempt == 0 {
						q.Enqueue([]byte{id})
					}
					_, err = q.Dequeue()
					require.NoError(t, err)
				}
				// Seed the first chunk so Sync reaches the application call before deliveries fill the buffer.
				added, err := q.Add(&chunk{Height: 3, Version: 1, ID: []byte{1}, Chunk: []byte{1}})
				require.NoError(t, err)
				require.True(t, added)
				ctx, cancel := context.WithCancel(context.Background())
				syncDone := make(chan error, 1)
				go func() { _, _, err := s.Sync(ctx, snap, q); syncDone <- err }()
				<-entered
				deliver := func(id byte) error {
					return r.handleChunkMessage(ctx, &p2p.Envelope{From: "peer", Message: &ssproto.ChunkResponse{
						Height: 3, Version: 1, ChunkId: []byte{id}, Chunk: []byte{id},
					}}, nil)
				}
				for id := byte(2); id <= 5; id++ {
					require.NoError(t, deliver(id))
				}
				addDone := make(chan error, 1)
				go func() { addDone <- deliver(6) }()
				waitForReceivedChunk(t, q, 6)
				if result == abci.ResponseApplySnapshotChunk_ABORT {
					cancel()
				}
				close(finish)
				select {
				case err := <-syncDone:
					expected := map[abci.ResponseApplySnapshotChunk_Result]error{
						abci.ResponseApplySnapshotChunk_ABORT:           errAbort,
						abci.ResponseApplySnapshotChunk_REJECT_SNAPSHOT: errRejectSnapshot,
						abci.ResponseApplySnapshotChunk_RETRY_SNAPSHOT:  errRetrySnapshot,
					}[result]
					require.ErrorIs(t, err, expected)
				case <-time.After(time.Second):
					// Drain a slot on failure so blocked deliveries can finish before cleanup.
					<-q.applyCh
					<-syncDone
					t.Error("Sync teardown blocked on a full chunk queue")
				}
				select {
				case <-addDone:
				case <-time.After(time.Second):
					t.Fatal("chunk delivery did not stop with the sync attempt")
				}
				cancel()
				require.Nil(t, s.chunkQueue)
				require.EqualValues(t, attempt+1, s.SnapshotChunksCount())
				if attempt+1 < attempts {
					q.RetryAll()
				}
			}
		})
	}
}

func TestChunkQueueCloseUnblocksAdd(t *testing.T) {
	q, err := newChunkQueue(&snapshot{Height: 3, Version: 1, Hash: []byte{1}}, t.TempDir(), 0)
	require.NoError(t, err)
	q.Enqueue([]byte{1})
	_, err = q.Dequeue()
	require.NoError(t, err)
	waiter := q.WaitFor([]byte{1})
	added := make(chan error, 1)
	go func() { _, err := q.Add(&chunk{Height: 3, Version: 1, ID: []byte{1}}); added <- err }()
	waitForReceivedChunk(t, q, 1)
	require.NoError(t, q.Close())
	require.NoError(t, q.Close())
	select {
	case err := <-added:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("Add blocked after Close")
	}
	_, ok := <-waiter
	require.False(t, ok)
	_, err = q.Next()
	require.ErrorIs(t, err, errDone)
	_, err = q.Add(&chunk{Height: 3, Version: 1, ID: []byte{1}})
	require.ErrorIs(t, err, errNilSnapshot)
	require.NoDirExists(t, q.dir)
}

func TestChunkQueueCloseUnblocksNext(t *testing.T) {
	q, err := newChunkQueue(&snapshot{Height: 3, Version: 1, Hash: []byte{1}}, t.TempDir(), 1)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { _, err := q.Next(); done <- err }()
	require.NoError(t, q.Close())
	select {
	case err := <-done:
		require.ErrorIs(t, err, errDone)
	case <-time.After(time.Second):
		t.Fatal("Next blocked after Close")
	}
}

func waitForReceivedChunk(t *testing.T, q *chunkQueue, id byte) {
	t.Helper()
	require.Eventually(t, func() bool {
		q.mtx.Lock()
		defer q.mtx.Unlock()
		return q.items[tmbytes.HexBytes{id}.String()].status == receivedStatus
	}, time.Second, time.Millisecond)
}

func TestSyncCancellationUnblocksFullChunkQueue(t *testing.T) {
	snap := &snapshot{Height: 3, Version: 1, Hash: []byte{1}}
	q, err := newChunkQueue(snap, t.TempDir(), 4)
	require.NoError(t, err)
	defer func() { require.NoError(t, q.Close()) }()
	provider := mocks.NewStateProvider(t)
	entered := make(chan struct{})
	provider.On("AppHash", mock.Anything, uint64(3)).Run(func(args mock.Arguments) {
		close(entered)
		<-args.Get(0).(context.Context).Done()
	}).Return(tmbytes.HexBytes(nil), context.Canceled).Once()
	s := &syncer{logger: log.NewNopLogger(), stateProvider: provider}
	r := &Reactor{logger: log.NewNopLogger(), syncer: s}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	syncDone := make(chan struct{})
	go func() {
		defer close(syncDone)
		_, _, _ = s.Sync(ctx, snap, q)
	}()
	<-entered
	// Establish requested chunks, exactly as fetchChunks does with Dequeue.
	for i := byte(1); i <= 5; i++ {
		q.Enqueue([]byte{i})
		_, err := q.Dequeue()
		require.NoError(t, err)
	}
	deliver := func(id byte) error {
		return r.handleChunkMessage(ctx, &p2p.Envelope{From: "peer", Message: &ssproto.ChunkResponse{
			Height: 3, Version: 1, ChunkId: []byte{id}, Chunk: []byte{id},
		}}, nil)
	}
	for i := byte(1); i <= 4; i++ {
		require.NoError(t, deliver(i))
	}
	addDone := make(chan struct{})
	go func() { defer close(addDone); _ = deliver(5) }()
	require.Eventually(t, func() bool {
		q.mtx.Lock()
		defer q.mtx.Unlock()
		return q.items[tmbytes.HexBytes{5}.String()].status == receivedStatus
	}, time.Second, time.Millisecond)
	cancel()
	select {
	case <-syncDone:
	case <-time.After(250 * time.Millisecond):
		t.Error("Sync failed to return after cancellation with a full chunk queue")
		<-q.applyCh
	}
	select {
	case <-addDone:
	case <-time.After(time.Second):
		t.Fatal("chunk handler did not exit after buffer drain")
	}
	select {
	case <-syncDone:
	case <-time.After(time.Second):
		t.Fatal("Sync did not exit after buffer drain")
	}
}

func TestChunkQueueCloseWithBufferedNext(t *testing.T) {
	q, err := newChunkQueue(&snapshot{Height: 3, Version: 1, Hash: []byte{1}}, t.TempDir(), 1)
	require.NoError(t, err)
	q.Enqueue([]byte{1})
	_, err = q.Dequeue()
	require.NoError(t, err)
	input := &chunk{Height: 3, Version: 1, ID: []byte{1}, Chunk: []byte{1}}
	added, err := q.Add(input)
	require.NoError(t, err)
	require.True(t, added)
	nextDone := make(chan struct{})
	go func() {
		defer close(nextDone)
		output, err := q.Next()
		if err == nil {
			assert.Equal(t, input, output)
		} else {
			assert.ErrorIs(t, err, errDone)
		}
	}()
	require.NoError(t, q.Close())
	select {
	case <-nextDone:
	case <-time.After(time.Second):
		t.Fatal("Next blocked while closing a buffered queue")
	}
	_, err = q.Next()
	require.ErrorIs(t, err, errDone)
}
