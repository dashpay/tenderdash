package p2p

import (
	"context"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	tmsync "github.com/dashpay/tenderdash/internal/libs/sync"
	"github.com/dashpay/tenderdash/internal/p2p/conn"
	dbm "github.com/dashpay/tenderdash/libs/db"
	"github.com/dashpay/tenderdash/libs/log"
	p2pproto "github.com/dashpay/tenderdash/proto/tendermint/p2p"
	"github.com/dashpay/tenderdash/types"
)

func TestRouterWaitDrainsAcceptedConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := dbm.NewMemDB()
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	manager, err := NewPeerManager(ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", db, PeerManagerOptions{})
	require.NoError(t, err)
	transport := NewMConnTransport(log.NewNopLogger(), conn.DefaultMConnConfig(), nil, MConnTransportOptions{})
	entered, release := make(chan struct{}), make(chan struct{})
	router, err := NewRouter(log.NewNopLogger(), NopMetrics(), nil, manager,
		func() *types.NodeInfo { return &types.NodeInfo{} }, transport,
		&Endpoint{Protocol: MConnProtocol, IP: net.IPv4(127, 0, 0, 1)}, RouterOptions{
			FilterPeerByIP: func(ctx context.Context, _ net.IP, _ uint16) error {
				close(entered)
				<-ctx.Done()
				<-release
				return ctx.Err()
			},
		})
	require.NoError(t, err)
	require.NoError(t, router.Start(ctx))
	t.Cleanup(func() { router.Stop(); router.Wait() })
	defer close(release)
	remote, err := net.Dial("tcp", transport.listener.Addr().String())
	require.NoError(t, err)
	defer remote.Close()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("connection filter did not start")
	}
	router.Stop()
	done := make(chan struct{})
	go func() { router.Wait(); close(done) }()
	select {
	case <-done:
		t.Fatal("Wait returned while an accepted connection was still being filtered")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestSimpleQueueClosedDrainsDeliveryCallback(t *testing.T) {
	q := newSimplePriorityQueue(context.Background(), 0)
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() { close(release); q.close(); <-q.closed() }()
	envelope := Envelope{Message: &p2pproto.PexRequest{}}
	envelope.EnableDeliveryNotification()
	envelope.delivery.setOnCompleted(func() { close(entered); <-release })
	q.enqueue() <- envelope
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("queue did not drop the envelope")
	}
	q.close()
	select {
	case <-q.closed():
		t.Fatal("queue reported completion before delivery callback returned")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestPeerUpdatesCloseJoinsUnsubscribe(t *testing.T) {
	db := dbm.NewMemDB()
	defer db.Close()
	manager, err := NewPeerManager(context.Background(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", db, PeerManagerOptions{})
	require.NoError(t, err)
	updates := manager.Subscribe(context.Background(), "lifecycle")
	manager.mtx.Lock()
	done := make(chan struct{})
	go func() { updates.Close(); close(done) }()
	select {
	case <-done:
		manager.mtx.Unlock()
		t.Fatal("Close returned before unsubscribing")
	case <-time.After(30 * time.Millisecond):
	}
	manager.mtx.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("subscription did not finish cleanup")
	}
	manager.mtx.Lock()
	require.Empty(t, manager.subscriptions)
	manager.mtx.Unlock()
	updates.Close()
}

// A reactor whose startup fails closes its subscription before consuming any
// update, possibly while Ready holds the mutex sending to its full buffer.
func TestPeerUpdatesCloseReleasesBlockedBroadcast(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := dbm.NewMemDB()
	defer db.Close()
	manager, err := NewPeerManager(ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", db, PeerManagerOptions{})
	require.NoError(t, err)
	updates := manager.Subscribe(ctx, "unconsumed")
	accept := func(c string) types.NodeID {
		id := types.NodeID(strings.Repeat(c, 40))
		_, err := manager.Add(NodeAddress{Protocol: "memory", NodeID: id})
		require.NoError(t, err)
		require.NoError(t, manager.Accepted(id))
		return id
	}
	for _, c := range []string{"b", "c", "d"}[:cap(updates.reactorUpdatesCh)] {
		manager.Ready(ctx, accept(c), nil)
	}
	blocked := accept("e")
	readyDone := make(chan struct{})
	go func() { defer close(readyDone); manager.Ready(ctx, blocked, nil) }()
	// Ready holds the mutex until its broadcast returns, and nothing else takes it.
	for manager.mtx.TryLock() {
		manager.mtx.Unlock()
		runtime.Gosched()
	}

	closed := make(chan struct{})
	go func() { updates.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited for a broadcast blocked on the closing subscription")
	}
	<-readyDone
}

type delayedAcceptListener struct {
	net.Listener
	entered chan struct{}
	release chan struct{}
}

func (l *delayedAcceptListener) Accept() (net.Conn, error) {
	close(l.entered)
	<-l.release
	return nil, net.ErrClosed
}

func (*delayedAcceptListener) Close() error { return nil }

func TestMConnTransportAcceptJoinsRequest(t *testing.T) {
	listener := &delayedAcceptListener{entered: make(chan struct{}), release: make(chan struct{})}
	transport := NewMConnTransport(log.NewNopLogger(), conn.DefaultMConnConfig(), nil, MConnTransportOptions{})
	transport.listener = listener
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _, _ = transport.Accept(ctx); close(done) }()
	defer func() { close(listener.release); <-done }()
	<-listener.entered
	cancel()
	select {
	case <-done:
		t.Fatal("Accept returned before its request worker finished")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestRouterManualStopClosesIndependentChannel(t *testing.T) {
	db := dbm.NewMemDB()
	defer db.Close()
	manager, err := NewPeerManager(context.Background(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", db, PeerManagerOptions{})
	require.NoError(t, err)
	transport := NewMConnTransport(log.NewNopLogger(), conn.DefaultMConnConfig(), nil, MConnTransportOptions{})
	info := &types.NodeInfo{Channels: tmsync.NewConcurrentSlice[uint16]()}
	router, err := NewRouter(log.NewNopLogger(), NopMetrics(), nil, manager,
		func() *types.NodeInfo { return info }, transport,
		&Endpoint{Protocol: MConnProtocol, IP: net.IPv4(127, 0, 0, 1)}, RouterOptions{})
	require.NoError(t, err)
	require.NoError(t, router.Start(context.Background()))
	t.Cleanup(func() { router.Stop(); router.Wait() })
	_, err = router.OpenChannel(context.Background(), &ChannelDescriptor{ID: 1, Priority: 1})
	require.NoError(t, err)
	router.Stop()
	router.Wait()
	router.channelMtx.RLock()
	count := len(router.channelQueues)
	router.channelMtx.RUnlock()
	require.Zero(t, count, "Wait must drain channels even when their caller context remains active")
	_, err = router.OpenChannel(context.Background(), &ChannelDescriptor{ID: 2, Priority: 1})
	require.Error(t, err)
}
