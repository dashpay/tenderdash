package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
	rpctypes "github.com/dashpay/tenderdash/rpc/jsonrpc/types"
)

func TestWebsocketManagerHandler(t *testing.T) {
	logger := log.NewNopLogger()

	s := newWSServer(t, logger)
	defer s.Close()

	t.Cleanup(leaktest.Check(t))

	// check upgrader works
	d := websocket.Dialer{}
	c, dialResp, err := d.Dial("ws://"+s.Listener.Addr().String()+"/websocket", nil)
	require.NoError(t, err)

	if got, want := dialResp.StatusCode, http.StatusSwitchingProtocols; got != want {
		t.Errorf("dialResp.StatusCode = %d, want %d", got, want)
	}

	// check basic functionality works
	req := rpctypes.NewRequest(1001)
	require.NoError(t, req.SetMethodAndParams("c", map[string]interface{}{"s": "a", "i": 10}))
	require.NoError(t, c.WriteJSON(req))

	var resp rpctypes.RPCResponse
	err = c.ReadJSON(&resp)
	require.NoError(t, err)
	require.Nil(t, resp.Error)
	dialResp.Body.Close()
}

func newWSServer(t *testing.T, logger log.Logger) *httptest.Server {
	type args struct {
		S string      `json:"s"`
		I json.Number `json:"i"`
	}
	funcMap := map[string]*RPCFunc{
		"c": NewWSRPCFunc(func(context.Context, *args) (string, error) { return "foo", nil }),
	}
	wm := NewWebsocketManager(logger, funcMap)

	mux := http.NewServeMux()
	mux.HandleFunc("/websocket", wm.WebsocketHandler)

	srv := httptest.NewServer(mux)

	t.Cleanup(srv.Close)

	return srv
}

func TestWebsocketStartJoinsHandler(t *testing.T) {
	type contextKey struct{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "inherited"))
	defer cancel()
	entered, canceled, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	disconnected := make(chan struct{})
	functions := map[string]*RPCFunc{"block": NewWSRPCFunc(func(ctx context.Context, _ *struct{}) (string, error) {
		if ctx.Value(contextKey{}) != "inherited" {
			t.Error("websocket handler lost parent context value")
		}
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return "done", nil
	})}
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		session := newWSConnection(conn, functions, log.NewNopLogger(), OnDisconnect(func(string) { close(disconnected) }))
		if err := session.Start(ctx); err != nil {
			t.Error(err)
		}
		close(done)
	}))
	defer server.Close()
	conn, response, err := websocket.DefaultDialer.Dial("ws://"+server.Listener.Addr().String(), nil)
	require.NoError(t, err)
	defer response.Body.Close()
	defer conn.Close()
	request := rpctypes.NewRequest(1)
	require.NoError(t, request.SetMethodAndParams("block", struct{}{}))
	require.NoError(t, conn.WriteJSON(request))
	<-entered
	cancel()
	<-canceled
	select {
	case <-done:
		close(release)
		t.Fatal("Start returned while request handler remained active")
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case <-disconnected:
		t.Error("subscription cleanup ran before request completed")
	default:
	}
	close(release)
	<-done
	<-disconnected
}

func TestServeJoinsWebsocketSessionWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	functions := map[string]*RPCFunc{
		"panic": NewWSRPCFunc(func(context.Context, *struct{}) (string, error) { panic("request panic") }),
		"subscribe": NewWSRPCFunc(func(ctx context.Context, _ *struct{}) (string, error) {
			if !rpctypes.GetCallInfo(ctx).WSConn.Go(func(ctx context.Context) {
				close(entered)
				<-ctx.Done()
				close(canceled)
				<-release
			}) {
				return "", context.Canceled
			}
			return "subscribed", nil
		}),
	}
	manager := NewWebsocketManager(log.NewNopLogger(), functions)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, listener, http.HandlerFunc(manager.WebsocketHandler), log.NewNopLogger(), DefaultConfig())
	}()
	conn, response, err := websocket.DefaultDialer.Dial("ws://"+listener.Addr().String(), nil)
	require.NoError(t, err)
	defer response.Body.Close()
	defer conn.Close()
	request := rpctypes.NewRequest(1)
	require.NoError(t, request.SetMethodAndParams("panic", struct{}{}))
	require.NoError(t, conn.WriteJSON(request))
	var result rpctypes.RPCResponse
	require.NoError(t, conn.ReadJSON(&result))
	require.NotNil(t, result.Error)
	require.NoError(t, request.SetMethodAndParams("subscribe", struct{}{}))
	require.NoError(t, conn.WriteJSON(request))
	<-entered
	cancel()
	<-canceled
	select {
	case err := <-done:
		close(release)
		t.Fatalf("Serve returned while websocket session work remained active: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	require.ErrorIs(t, <-done, http.ErrServerClosed)
}
