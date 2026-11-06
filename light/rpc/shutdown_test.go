package rpc

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
	rpchttp "github.com/dashpay/tenderdash/rpc/client/http"
	rpcserver "github.com/dashpay/tenderdash/rpc/jsonrpc/server"
)

// OnDrain stops an HTTP-backed upstream client, which may already have stopped
// itself, as it does when its reconnect attempts run out.
func TestClientDrainToleratesStoppedUpstream(t *testing.T) {
	logger := log.NewNopLogger()
	ws := rpcserver.NewWebsocketManager(logger, map[string]*rpcserver.RPCFunc{})
	mux := http.NewServeMux()
	mux.HandleFunc("/websocket", ws.WebsocketHandler)
	upstream := httptest.NewServer(mux)
	t.Cleanup(upstream.Close)
	next, err := rpchttp.New(upstream.URL)
	require.NoError(t, err)
	client := NewClient(logger, next, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, client.Start(ctx))

	cancel()
	client.Wait()
	require.NoError(t, next.Stop())
}

// Cancellation must end the client even when its upstream hangs during a redial.
func TestClientWaitDuringHungUpstreamRedial(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var accepted atomic.Int32
	upgrader := websocket.Upgrader{}
	srv := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if accepted.Add(1) == 1 {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err == nil {
					_ = conn.UnderlyingConn().Close() // the client sees 1006 and redials
				}
				return
			}
			<-r.Context().Done() // never answers
		}),
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	next, err := rpchttp.New("http://" + ln.Addr().String())
	require.NoError(t, err)
	client := NewClient(log.NewNopLogger(), next, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, client.Start(ctx))
	require.Eventually(t, func() bool { return accepted.Load() == 2 }, 10*time.Second, time.Millisecond)

	cancel()
	waited := make(chan struct{})
	go func() { client.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("light client Wait hung after cancellation")
	}
}
