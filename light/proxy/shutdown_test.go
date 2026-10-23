package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
	lrpc "github.com/dashpay/tenderdash/light/rpc"
	rpchttp "github.com/dashpay/tenderdash/rpc/client/http"
	rpcserver "github.com/dashpay/tenderdash/rpc/jsonrpc/server"
)

// Serve joins WebSocket disconnect cleanup. With the upstream client's writer
// already stopped, that cleanup must not block proxy shutdown.
func TestProxyShutdownWithStoppedUpstreamClient(t *testing.T) {
	logger := log.NewNopLogger()
	upstreamWS := rpcserver.NewWebsocketManager(logger, map[string]*rpcserver.RPCFunc{})
	upstreamMux := http.NewServeMux()
	upstreamMux.HandleFunc("/websocket", upstreamWS.WebsocketHandler)
	upstream := httptest.NewServer(upstreamMux)
	t.Cleanup(upstream.Close)

	next, err := rpchttp.New(upstream.URL)
	require.NoError(t, err)
	client := lrpc.NewClient(logger, next, nil)
	clientCtx, stopClient := context.WithCancel(context.Background())
	t.Cleanup(stopClient)
	require.NoError(t, client.Start(clientCtx))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	p := &Proxy{Addr: "tcp://127.0.0.1:0", Config: rpcserver.DefaultConfig(), Client: client, Logger: logger}
	listener, mux, err := p.listen(ctx)
	require.NoError(t, err)
	served := make(chan error, 1)
	go func() { served <- rpcserver.Serve(ctx, listener, mux, logger, p.Config) }()

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, resp, err := dialer.Dial("ws://"+listener.Addr().String()+"/websocket", nil)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	t.Cleanup(func() { _ = conn.Close() })

	// Stop the upstream client first; Wait returns after its writer exits.
	stopClient()
	client.Wait()

	cancel()
	select {
	case err := <-served:
		require.ErrorIs(t, err, http.ErrServerClosed)
	case <-time.After(5 * time.Second):
		t.Fatal("proxy shutdown blocked on disconnect cleanup through a stopped upstream client")
	}
}
