package p2p

import (
	"context"
	"os"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

func TestRouter_ReadyAfterQueue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	selfID := types.NodeID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	peerID := types.NodeID("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	db := dbm.NewMemDB()
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	manager, err := NewPeerManager(ctx, selfID, db, PeerManagerOptions{})
	require.NoError(t, err)
	require.NoError(t, manager.Accepted(peerID))
	updates := manager.Subscribe(ctx, "queue-readiness-test")
	router, err := NewRouter(log.NewNopLogger(), NopMetrics(), nil, manager,
		func() *types.NodeInfo { return &types.NodeInfo{NodeID: selfID} }, nil, nil, RouterOptions{})
	require.NoError(t, err)

	queueStarted := make(chan struct{})
	releaseQueue := make(chan struct{})
	router.queueFactory = func(size int) queue {
		close(queueStarted)
		select {
		case <-releaseQueue:
		case <-ctx.Done():
		}
		return newFIFOQueue(size)
	}
	conn := &MemoryConnection{logger: log.NewNopLogger(), closeFn: func() {}}
	routed := make(chan struct{})
	go func() {
		defer close(routed)
		router.routePeer(ctx, peerID, conn, ChannelIDSet{1: {}})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-routed:
		case <-time.After(5 * time.Second):
			t.Error("peer routing did not stop")
		}
	})

	select {
	case <-queueStarted:
	case <-ctx.Done():
		t.Fatal("queue initialization did not start")
	}
	select {
	case update := <-updates.Updates():
		t.Fatalf("peer advertised readiness before its send queue exists: %+v", update)
	default:
	}
	close(releaseQueue)
	select {
	case update := <-updates.Updates():
		require.Equal(t, PeerStatusUp, update.Status)
		require.Equal(t, peerID, update.NodeID)
		require.NotZero(t, update.ConnID)
		router.peerMtx.RLock()
		_, queued := router.peerQueues[peerID]
		_, hasChannel := router.peerChannels[peerID][1]
		router.peerMtx.RUnlock()
		require.True(t, queued)
		require.True(t, hasChannel)
	case <-ctx.Done():
		t.Fatal("initialized peer did not become ready")
	}
}

func TestRouter_ConstructQueueFactory(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	t.Run("ValidateOptionsPopulatesDefaultQueue", func(t *testing.T) {
		opts := RouterOptions{}
		require.NoError(t, opts.Validate())
		require.Equal(t, "fifo", opts.QueueType)
	})
	t.Run("Default", func(t *testing.T) {
		require.Zero(t, os.Getenv("TM_P2P_QUEUE"))
		opts := RouterOptions{}
		r, err := NewRouter(log.NewNopLogger(), nil, nil, nil, func() *types.NodeInfo { return &types.NodeInfo{} }, nil, nil, opts)
		require.NoError(t, err)
		require.NoError(t, r.setupQueueFactory(ctx))

		_, ok := r.queueFactory(1).(*fifoQueue)
		require.True(t, ok)
	})
	t.Run("Fifo", func(t *testing.T) {
		opts := RouterOptions{QueueType: queueTypeFifo}
		r, err := NewRouter(log.NewNopLogger(), nil, nil, nil, func() *types.NodeInfo { return &types.NodeInfo{} }, nil, nil, opts)
		require.NoError(t, err)
		require.NoError(t, r.setupQueueFactory(ctx))

		_, ok := r.queueFactory(1).(*fifoQueue)
		require.True(t, ok)
	})
	t.Run("Priority", func(t *testing.T) {
		opts := RouterOptions{QueueType: queueTypePriority}
		r, err := NewRouter(log.NewNopLogger(), nil, nil, nil, func() *types.NodeInfo { return &types.NodeInfo{} }, nil, nil, opts)
		require.NoError(t, err)
		require.NoError(t, r.setupQueueFactory(ctx))

		q, ok := r.queueFactory(1).(*pqScheduler)
		require.True(t, ok)
		defer q.close()
	})
	t.Run("NonExistant", func(t *testing.T) {
		opts := RouterOptions{QueueType: "fast"}
		_, err := NewRouter(log.NewNopLogger(), nil, nil, nil, func() *types.NodeInfo { return &types.NodeInfo{} }, nil, nil, opts)
		require.Error(t, err)
		require.Contains(t, err.Error(), "fast")
	})
	t.Run("InternalsSafeWhenUnspecified", func(t *testing.T) {
		r := &Router{}
		require.Zero(t, r.options.QueueType)

		fn, err := r.createQueueFactory(ctx)
		require.Error(t, err)
		require.Nil(t, fn)
	})
}
