package rpc

import (
	"io"
	stdlog "log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/rpc/core"
	"github.com/dashpay/tenderdash/libs/log"
)

// TestHandlerWebsocketOriginPolicy ensures the inspect RPC websocket upgrade
// honors the operator's configured CORSAllowedOrigins, matching the HTTP CORS
// path and the node RPC. Without that wiring the websocket default
// (same-host/Origin-less only) silently 403s allow-listed browser origins that
// the HTTP endpoint accepts.
func TestHandlerWebsocketOriginPolicy(t *testing.T) {
	const allowed = "http://dash.org"

	rpcConfig := config.TestRPCConfig()
	rpcConfig.CORSAllowedOrigins = []string{allowed}

	handler := Handler(rpcConfig, core.RoutesMap{}, log.NewNopLogger())

	srv := httptest.NewUnstartedServer(handler)
	// Silence the recovered disconnect-path log: tearing down a websocket
	// triggers a pre-existing nil eventBus panic in Handler's OnDisconnect
	// callback (tracked separately, not part of this change). The panic is
	// recovered by net/http; we only assert the upgrade outcome here.
	srv.Config.ErrorLog = stdlog.New(io.Discard, "", 0)
	srv.Start()
	defer srv.Close()

	wsURL := "ws://" + srv.Listener.Addr().String() + "/websocket"

	testCases := []struct {
		name        string
		origin      string
		wantUpgrade bool
	}{
		{name: "no origin upgraded", origin: "", wantUpgrade: true},
		{name: "same host upgraded", origin: "http://" + srv.Listener.Addr().String(), wantUpgrade: true},
		{name: "allow-listed cross origin upgraded", origin: allowed, wantUpgrade: true},
		{name: "non-listed cross origin rejected", origin: "http://evil.com", wantUpgrade: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var header http.Header
			if tc.origin != "" {
				header = http.Header{"Origin": []string{tc.origin}}
			}

			conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
			if resp != nil {
				defer resp.Body.Close()
			}
			if tc.wantUpgrade {
				require.NoError(t, err)
				require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
				require.NoError(t, conn.Close())
				return
			}
			require.Error(t, err)
			require.NotNil(t, resp)
			assert.Equal(t, http.StatusForbidden, resp.StatusCode)
		})
	}
}
