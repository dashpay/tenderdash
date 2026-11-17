package http_test

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/eventbus"
	rpccore "github.com/dashpay/tenderdash/internal/rpc/core"
	"github.com/dashpay/tenderdash/libs/log"
	rpchttp "github.com/dashpay/tenderdash/rpc/client/http"
	"github.com/dashpay/tenderdash/rpc/coretypes"
	rpcserver "github.com/dashpay/tenderdash/rpc/jsonrpc/server"
	"github.com/dashpay/tenderdash/types"
)

type lockedBuffer struct {
	mtx sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	return b.buf.String()
}

// connListener records accepted connections, so a test can drop WebSocket
// sessions, which httptest stops tracking once they are hijacked.
type connListener struct {
	net.Listener
	mtx   sync.Mutex
	conns []net.Conn
}

func (l *connListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err == nil {
		l.mtx.Lock()
		l.conns = append(l.conns, conn)
		l.mtx.Unlock()
	}
	return conn, err
}

func (l *connListener) dropAll(t *testing.T) {
	t.Helper()
	l.mtx.Lock()
	defer l.mtx.Unlock()
	for _, conn := range l.conns {
		_ = conn.Close()
	}
	l.conns = nil
}

// Every reconnect must resubscribe exactly the client's queries, so a node
// accepts all of them and events keep flowing without errors.
func TestReconnectResubscribesOnlyQueries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := log.NewNopLogger()
	bus := eventbus.NewDefault(logger)
	require.NoError(t, bus.Start(ctx))
	t.Cleanup(func() { cancel(); joinWithin(t, "event bus", bus.Wait) })
	env := &rpccore.Environment{EventBus: bus, Config: *config.DefaultRPCConfig(), Logger: logger}
	// As the node does, a closed session's subscriptions are dropped.
	wm := rpcserver.NewWebsocketManager(logger, rpccore.NewRoutesMap(env, nil),
		rpcserver.OnDisconnect(func(remoteAddr string) { _ = bus.UnsubscribeAll(ctx, remoteAddr) }))
	mux := http.NewServeMux()
	mux.HandleFunc("/websocket", wm.WebsocketHandler)
	srv := httptest.NewUnstartedServer(mux)
	listener := &connListener{Listener: srv.Listener}
	srv.Listener = listener
	srv.Start()
	t.Cleanup(srv.Close)

	client, err := rpchttp.New(srv.URL)
	require.NoError(t, err)
	logs := &lockedBuffer{}
	client.Logger, err = log.NewLogger(log.LogLevelError, logs)
	require.NoError(t, err)
	require.NoError(t, client.Start(ctx))
	out, err := client.Subscribe(ctx, "test", types.EventQueryNewRound.String())
	require.NoError(t, err)
	// Unread events would stall the client's single event loop.
	events := make(chan coretypes.ResultEvent, 1000)
	forwarded := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		joinWithin(t, "client", func() { require.NoError(t, client.Stop()) })
		joinWithin(t, "event forwarder", func() { <-forwarded })
	})
	go func() {
		defer close(forwarded)
		for {
			select {
			case event := <-out:
				events <- event
			case <-ctx.Done():
				return
			}
		}
	}()

	for session := 1; session <= 3; session++ {
		requireNewRound(t, bus, events)
		if session < 3 {
			listener.dropAll(t)
			// The new session is counted once the client resubscribes there.
			require.Eventually(t, func() bool { return bus.NumClients() == 0 }, 5*time.Second, time.Millisecond)
			require.Eventually(t, func() bool { return bus.NumClients() == 1 }, 10*time.Second, time.Millisecond)
			for len(events) > 0 {
				<-events // delivered before the drop
			}
		}
	}
	require.NotContains(t, logs.String(), "WS error")
}

func joinWithin(t *testing.T, what string, join func()) {
	t.Helper()
	joined := make(chan struct{})
	go func() { defer close(joined); join() }()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Errorf("%s did not stop", what)
	}
}

// requireNewRound publishes until an event arrives, as (re)subscribing is
// asynchronous.
func requireNewRound(t *testing.T, bus *eventbus.EventBus, events <-chan coretypes.ResultEvent) {
	t.Helper()
	timeout := time.After(10 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-events:
			return
		case <-ticker.C:
			require.NoError(t, bus.PublishEventNewRound(types.EventDataNewRound{}))
		case <-timeout:
			t.Fatal("no event delivered")
		}
	}
}
