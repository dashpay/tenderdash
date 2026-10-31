package proxy

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
	lrpc "github.com/dashpay/tenderdash/light/rpc"
	rpchttp "github.com/dashpay/tenderdash/rpc/client/http"
	rpcserver "github.com/dashpay/tenderdash/rpc/jsonrpc/server"
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

// Serve joins WebSocket disconnect cleanup. With the upstream client's writer
// already stopped, that cleanup must not block proxy shutdown, whether the proxy
// context ends or only the listener closes and leaves that context live.
func TestProxyShutdownWithStoppedUpstreamClient(t *testing.T) {
	testCases := map[string]struct {
		shutdown         func(t *testing.T, cancel context.CancelFunc, listener net.Listener)
		serveErr         error
		cleanupErrLogged bool
	}{
		"proxy context canceled": {
			shutdown: func(_ *testing.T, cancel context.CancelFunc, _ net.Listener) { cancel() },
			serveErr: http.ErrServerClosed,
		},
		"listener closed": {
			shutdown: func(t *testing.T, _ context.CancelFunc, listener net.Listener) {
				require.NoError(t, listener.Close())
			},
			serveErr:         net.ErrClosed,
			cleanupErrLogged: true,
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
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
			logs := &lockedBuffer{}
			proxyLogger, err := log.NewLogger(log.LogLevelError, logs)
			require.NoError(t, err)
			p := &Proxy{Addr: "tcp://127.0.0.1:0", Config: rpcserver.DefaultConfig(), Client: client, Logger: proxyLogger}
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

			tc.shutdown(t, cancel, listener)
			select {
			case err := <-served:
				require.ErrorIs(t, err, tc.serveErr)
			case <-time.After(5 * time.Second):
				t.Fatal("proxy shutdown blocked on disconnect cleanup through a stopped upstream client")
			}
			if tc.cleanupErrLogged {
				require.Contains(t, logs.String(), "Failed to unsubscribe addr from events")
			} else {
				require.NotContains(t, logs.String(), "Failed to unsubscribe addr from events")
			}
		})
	}
}
