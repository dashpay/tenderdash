package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/fortytw2/leaktest"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rpctypes "github.com/dashpay/tenderdash/rpc/jsonrpc/types"
)

const wsCallTimeout = 5 * time.Second

type myTestHandler struct {
	closeConnAfterRead bool
	mtx                sync.RWMutex
	t                  *testing.T
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func (h *myTestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	require.NoError(h.t, err)

	defer conn.Close()
	for {
		messageType, in, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var req rpctypes.RPCRequest
		err = json.Unmarshal(in, &req)
		require.NoError(h.t, err)

		func() {
			h.mtx.RLock()
			defer h.mtx.RUnlock()

			if h.closeConnAfterRead {
				require.NoError(h.t, conn.Close())
			}
		}()

		res := json.RawMessage(`{}`)

		emptyRespBytes, err := json.Marshal(req.MakeResponse(res))
		require.NoError(h.t, err)
		if err := conn.WriteMessage(messageType, emptyRespBytes); err != nil {
			return
		}
	}
}

func TestWSClientReconnectsAfterReadFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	t.Cleanup(leaktest.Check(t))

	// start server
	h := &myTestHandler{t: t}
	s := httptest.NewServer(h)
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c := startClient(ctx, t, "//"+s.Listener.Addr().String())

	go handleResponses(ctx, t, c)

	h.mtx.Lock()
	h.closeConnAfterRead = true
	h.mtx.Unlock()

	// results in WS read error, no send retry because write succeeded
	call(ctx, t, "a", c)

	// expect to reconnect almost immediately
	time.Sleep(10 * time.Millisecond)
	h.mtx.Lock()
	h.closeConnAfterRead = false
	h.mtx.Unlock()

	// should succeed
	call(ctx, t, "b", c)
}

func TestWSClientReconnectsAfterWriteFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	t.Cleanup(leaktest.Check(t))

	// start server
	h := &myTestHandler{t: t}
	s := httptest.NewServer(h)
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := startClient(ctx, t, "//"+s.Listener.Addr().String())

	go handleResponses(ctx, t, c)

	// hacky way to abort the connection before write
	if err := c.conn.Close(); err != nil {
		t.Error(err)
	}

	// results in WS write error, the client should resend on reconnect
	call(ctx, t, "a", c)

	// expect to reconnect almost immediately
	time.Sleep(10 * time.Millisecond)

	// should succeed
	call(ctx, t, "b", c)
}

func TestWSClientReconnectFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	t.Cleanup(leaktest.Check(t))

	// start server
	h := &myTestHandler{t: t}
	s := httptest.NewServer(h)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := startClient(ctx, t, "//"+s.Listener.Addr().String())

	go func() {
		for {
			select {
			case <-c.ResponsesCh:
			case <-ctx.Done():
				return
			}
		}
	}()

	// hacky way to abort the connection before write
	if err := c.conn.Close(); err != nil {
		t.Error(err)
	}
	s.Close()

	// results in WS write error
	// provide timeout to avoid blocking
	cctx, cancel := context.WithTimeout(ctx, wsCallTimeout)
	defer cancel()
	if err := c.Call(cctx, "a", make(map[string]interface{})); err != nil {
		t.Error(err)
	}

	// expect to reconnect almost immediately
	time.Sleep(10 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		// client should block on this
		call(ctx, t, "b", c)
		close(done)
	}()

	// test that client blocks on the second send
	select {
	case <-done:
		t.Fatal("client should block on calling 'b' during reconnect")
	case <-time.After(5 * time.Second):
		t.Log("All good")
	}
}

func TestNotBlockingOnStop(t *testing.T) {
	t.Cleanup(leaktest.Check(t))

	s := httptest.NewServer(&myTestHandler{t: t})
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := startClient(ctx, t, "//"+s.Listener.Addr().String())
	require.NoError(t, c.Call(ctx, "a", make(map[string]interface{})))

	time.Sleep(200 * time.Millisecond) // give service routines time to start ⚠️
	done := make(chan struct{})
	go func() {
		cancel()
		if assert.NoError(t, c.Stop()) {
			close(done)
		}
	}()
	select {
	case <-done:
		t.Log("Stopped client successfully")
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for client to stop")
	}
}

func startClient(ctx context.Context, t *testing.T, addr string) *WSClient {
	t.Helper()

	t.Cleanup(leaktest.Check(t))

	c, err := NewWS(addr, "/websocket")
	require.NoError(t, err)
	require.NoError(t, c.Start(ctx))
	return c
}

func call(ctx context.Context, t *testing.T, method string, c *WSClient) {
	t.Helper()

	err := c.Call(ctx, method, make(map[string]interface{}))
	if ctx.Err() == nil {
		require.NoError(t, err)
	}
}

func handleResponses(ctx context.Context, t *testing.T, c *WSClient) {
	t.Helper()

	for {
		select {
		case resp := <-c.ResponsesCh:
			if resp.Error != nil {
				t.Errorf("unexpected error: %v", resp.Error)
				return
			}
			if resp.Result != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// startFailingClient starts a client that gives up after its first failed redial,
// and returns it with a server that drops the connection on any request.
func startFailingClient(ctx context.Context, t *testing.T) (*WSClient, *httptest.Server) {
	t.Helper()
	s := httptest.NewServer(&myTestHandler{t: t, closeConnAfterRead: true})
	t.Cleanup(s.Close)
	c, err := NewWS("//"+s.Listener.Addr().String(), "/websocket")
	require.NoError(t, err)
	c.maxReconnectAttempts = 0
	require.NoError(t, c.Start(ctx))
	return c, s
}

func requireResponsesClosed(t *testing.T, c *WSClient) {
	t.Helper()
	select {
	case _, ok := <-c.ResponsesCh:
		require.False(t, ok, "unexpected response")
	case <-time.After(wsCallTimeout):
		t.Fatal("ResponsesCh was not closed")
	}
}

// Running out of reconnect attempts shuts the client down by itself. The owner
// does not know that, and still calls Stop once its context ends.
func TestWSClientStopAfterReconnectExhaustion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, s := startFailingClient(ctx, t)
	require.NoError(t, c.Call(ctx, "drop", make(map[string]interface{})))
	s.Close()
	requireResponsesClosed(t, c)

	cancel()
	require.NoError(t, c.Stop())
	require.NoError(t, c.Stop())
}

// A Stop already blocked when the attempts run out must finish with that shutdown.
func TestWSClientStopRacingReconnectExhaustion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, s := startFailingClient(ctx, t)
	require.NoError(t, c.Call(ctx, "drop", make(map[string]interface{})))
	s.Close()
	require.Eventually(t, c.IsReconnecting, wsCallTimeout, time.Millisecond)

	stopped := make(chan error, 2)
	for range cap(stopped) {
		go func() { stopped <- c.Stop() }()
	}
	for range cap(stopped) {
		select {
		case err := <-stopped:
			require.NoError(t, err)
			requireResponsesClosed(t, c)
		case <-time.After(wsCallTimeout):
			t.Fatal("Stop did not return after the attempts ran out")
		}
	}
}

// A reconnect can start new workers, so Stop must not finish while one may still
// succeed. Every concurrent Stop returns only after the channel closes.
func TestWSClientStopWaitsForReconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, _ := startFailingClient(ctx, t)
	require.NoError(t, c.Call(ctx, "drop", make(map[string]interface{})))
	require.Eventually(t, c.IsReconnecting, wsCallTimeout, time.Millisecond)

	stopped := make(chan error, 2)
	for range cap(stopped) {
		go func() { stopped <- c.Stop() }()
	}
	require.Eventually(t, func() bool { return !c.IsReconnecting() }, wsCallTimeout, time.Millisecond)
	select {
	case err := <-stopped:
		t.Fatalf("Stop returned before the context ended: %v", err)
	default:
	}

	cancel()
	for range cap(stopped) {
		select {
		case err := <-stopped:
			require.NoError(t, err)
			requireResponsesClosed(t, c)
		case <-time.After(wsCallTimeout):
			t.Fatal("Stop did not return after the context ended")
		}
	}
}

// A client that never started has nothing to wait for.
func TestWSClientStopWithoutStart(t *testing.T) {
	c, err := NewWS("//127.0.0.1:1", "/websocket")
	require.NoError(t, err)
	stopped := make(chan error, 1)
	go func() { stopped <- c.Stop() }()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(wsCallTimeout):
		t.Fatal("Stop blocked on a client that never started")
	}
}
