package client

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

func requireStopReturns(t *testing.T, c *WSClient) {
	t.Helper()
	stopped := make(chan error, 1)
	go func() { stopped <- c.Stop() }()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(wsCallTimeout):
		t.Fatal("Stop did not return")
	}
}

// listenWithoutAnswering returns the address of a peer that never answers the
// upgrade, and a channel signaled once a client has sent its upgrade request.
func listenWithoutAnswering(t *testing.T) (string, <-chan net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	accepted := make(chan net.Conn, 8)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
			if _, err := conn.Read(make([]byte, 1)); err == nil {
				accepted <- conn
			}
		}
	}()
	return "//" + ln.Addr().String(), accepted
}

// The first dial observes the context passed to Start.
func TestWSClientStartCanceledDuringHungHandshake(t *testing.T) {
	addr, accepted := listenWithoutAnswering(t)
	c, err := NewWS(addr, "/websocket")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan error, 1)
	go func() { started <- c.Start(ctx) }()
	<-accepted

	cancel()
	select {
	case err := <-started:
		require.Error(t, err)
	case <-time.After(wsCallTimeout):
		t.Fatal("Start blocked on a hung handshake after its context ended")
	}
	require.NoError(t, c.Stop())
}

// A peer that never answers cannot block a dial forever, even with a live context.
func TestWSClientHandshakeTimesOut(t *testing.T) {
	addr, _ := listenWithoutAnswering(t)
	c, err := NewWS(addr, "/websocket")
	require.NoError(t, err)
	c.handshakeTimeout = 50 * time.Millisecond
	started := make(chan error, 1)
	go func() { started <- c.Start(context.Background()) }()
	select {
	case err := <-started:
		require.Error(t, err)
	case <-time.After(wsCallTimeout):
		t.Fatal("handshake did not time out")
	}
}

// Stop follows the context even when a redial is stuck in its handshake.
func TestWSClientStopDuringHungRedial(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var accepted atomic.Int32
	srv := &http.Server{
		ReadHeaderTimeout: wsCallTimeout,
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := NewWS("//"+ln.Addr().String(), "/websocket")
	require.NoError(t, err)
	require.NoError(t, c.Start(ctx))
	require.Eventually(t, func() bool { return accepted.Load() == 2 }, 2*wsCallTimeout, time.Millisecond)

	cancel()
	requireStopReturns(t, c)
}

// A writer stuck in a write outlives its context and the reconnect routine. Stop
// must still join it, and it must not block reporting the error once it fails.
func TestWSClientStopJoinsWorkerFailingAfterReconnectExit(t *testing.T) {
	serverConn := make(chan *websocket.Conn, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			serverConn <- conn // never reads, so the client's send path fills
		}
	}))
	t.Cleanup(s.Close)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := NewWS("//"+s.Listener.Addr().String(), "/websocket")
	require.NoError(t, err)
	require.NoError(t, c.Start(ctx))
	peer := <-serverConn
	t.Cleanup(func() { _ = peer.Close() })
	// Call returns once the writer has taken the request, too big for socket buffers.
	require.NoError(t, c.Call(ctx, "big", map[string]interface{}{"x": strings.Repeat("a", 64<<20)}))
	cancel()
	select {
	case <-c.reconnectDone:
	case <-time.After(wsCallTimeout):
		t.Fatal("reconnect routine did not exit")
	}

	stopped := make(chan error, 1)
	go func() { stopped <- c.Stop() }()
	// Nothing observable marks Stop blocking; this window cannot fail correct code.
	select {
	case err := <-stopped:
		t.Fatalf("Stop returned while the writer could still use the client: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	// The reader reports this and closes the connection, so the write fails too.
	require.NoError(t, peer.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseGoingAway, "bye"), time.Now().Add(time.Second)))
	// Encoding the request is slow under -race and load, and the writer cannot stop
	// until it has finished. This only needs to tell that from a hang.
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(6 * wsCallTimeout):
		t.Fatal("Stop did not return after the workers failed")
	}
}

// Each run gets its own shutdown.
func TestWSClientRestartAfterStop(t *testing.T) {
	s := httptest.NewServer(&myTestHandler{t: t})
	t.Cleanup(s.Close)
	c, err := NewWS("//"+s.Listener.Addr().String(), "/websocket")
	require.NoError(t, err)
	for range 2 {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		require.NoError(t, c.Start(ctx))
		cancel()
		requireStopReturns(t, c)
		requireResponsesClosed(t, c)
	}
}

// The dialer shared with the HTTP client observes its context, so a cancellation
// does not wait out the connect timeout of an unreachable peer.
func TestHTTPDialerObservesContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := makeHTTPDialer("tcp", "127.0.0.1:1")(ctx, "", "")
	require.ErrorIs(t, err, context.Canceled)
}

// Stop joins a reconnect callback already running, which may still use the client.
func TestWSClientStopJoinsReconnectCallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := httptest.NewServer(&myTestHandler{t: t, closeConnAfterRead: true})
	t.Cleanup(s.Close)
	c, err := NewWS("//"+s.Listener.Addr().String(), "/websocket")
	require.NoError(t, err)
	entered := make(chan struct{})
	release := make(chan struct{})
	c.OnReconnect(func() {
		close(entered)
		<-release
	})
	require.NoError(t, c.Start(ctx))
	require.NoError(t, c.Call(ctx, "drop", make(map[string]interface{})))
	select {
	case <-entered:
	case <-time.After(2 * wsCallTimeout):
		close(release)
		t.Fatal("reconnect callback did not run")
	}

	cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- c.Stop() }()
	// Nothing observable marks Stop blocking; this window cannot fail correct code.
	select {
	case err := <-stopped:
		close(release)
		t.Fatalf("Stop returned before the reconnect callback finished: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(wsCallTimeout):
		t.Fatal("Stop did not return after the reconnect callback finished")
	}
}

// A send needs the writer, so it cannot wait for one that will never return.
func TestWSClientSendFailsAfterShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := httptest.NewServer(&myTestHandler{t: t})
	t.Cleanup(s.Close)
	c, err := NewWS("//"+s.Listener.Addr().String(), "/websocket")
	require.NoError(t, err)
	require.NoError(t, c.Start(ctx))
	cancel()
	require.NoError(t, c.Stop())

	sent := make(chan error, 1)
	go func() { sent <- c.Call(context.Background(), "late", make(map[string]interface{})) }()
	select {
	case err := <-sent:
		require.ErrorIs(t, err, errClientStopped)
	case <-time.After(wsCallTimeout):
		t.Fatal("Send blocked on a client that has shut down")
	}
}
