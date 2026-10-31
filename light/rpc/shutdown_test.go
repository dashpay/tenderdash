package rpc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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
