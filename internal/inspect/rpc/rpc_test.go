package rpc_test

import (
	"net/http/httptest"
	"testing"

	"github.com/fortytw2/leaktest"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/inspect/rpc"
	rpccore "github.com/dashpay/tenderdash/internal/rpc/core"
	"github.com/dashpay/tenderdash/libs/log"
)

// TestHandlerWebsocketDisconnectNoPanic verifies that closing a WebSocket
// connection does not panic when no event bus is wired into the handler.
// The inspect server has no subscribe routes, so the nil-eventBus guard
// must make the disconnect callback a safe no-op.
func TestHandlerWebsocketDisconnectNoPanic(t *testing.T) {
	t.Cleanup(leaktest.Check(t))

	cfg := config.TestRPCConfig()
	routes := rpccore.RoutesMap{}
	logger := log.NewNopLogger()

	h := rpc.Handler(cfg, routes, logger)
	require.NotNil(t, h)

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	d := websocket.Dialer{}
	conn, _, err := d.Dial("ws://"+srv.Listener.Addr().String()+"/websocket", nil)
	require.NoError(t, err)

	// A proper close handshake causes the server's readRoutine to call
	// wsc.Stop() → onDisconnect. Without the nil guard, this panics.
	_ = conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
	)
	conn.Close()
}
