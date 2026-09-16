package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
	lrpc "github.com/dashpay/tenderdash/light/rpc"
	"github.com/dashpay/tenderdash/rpc/client/mocks"
	rpcserver "github.com/dashpay/tenderdash/rpc/jsonrpc/server"
)

func TestProxyWebsocketOrigins(t *testing.T) {
	for _, tc := range []struct {
		name    string
		origins []string
		origin  string
		allowed bool
	}{
		{name: "non-browser", allowed: true},
		{name: "unconfigured browser", origin: "https://app.example"},
		{name: "configured browser", origins: []string{"https://app.example"}, origin: "https://app.example", allowed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			next := mocks.NewClient(t)
			next.On("Start", mock.Anything).Return(nil).Once()
			next.On("UnsubscribeAll", mock.Anything, mock.Anything).Return(nil).Maybe()
			logger := log.NewNopLogger()
			client := lrpc.NewClient(logger, next, nil)
			p := &Proxy{
				Addr: "tcp://127.0.0.1:0", Config: rpcserver.DefaultConfig(),
				Client: client, Logger: logger, AllowedOrigins: tc.origins,
			}
			listener, mux, err := p.listen(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, listener.Close()) })
			t.Cleanup(client.Stop)
			server := httptest.NewServer(mux)
			t.Cleanup(server.Close)
			headers := http.Header{}
			if tc.origin != "" {
				headers.Set("Origin", tc.origin)
			}
			dialer := websocket.Dialer{HandshakeTimeout: time.Second}
			conn, response, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/websocket", headers)
			if response != nil {
				t.Cleanup(func() { require.NoError(t, response.Body.Close()) })
			}
			if tc.allowed {
				require.NoError(t, err)
				require.NoError(t, conn.Close())
			} else {
				require.Error(t, err)
				require.NotNil(t, response)
				require.Equal(t, http.StatusForbidden, response.StatusCode)
			}
		})
	}
}
