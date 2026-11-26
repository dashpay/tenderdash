package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	rpctypes "github.com/dashpay/tenderdash/rpc/jsonrpc/types"
)

// orderedUpstream records every request on the current session, in order, and
// models the upstream subscription state per query.
type orderedUpstream struct {
	mtx        sync.Mutex
	conn       *websocket.Conn
	requests   []string
	subscribed map[string]bool
	redial     chan struct{} // if set, a reconnect waits until it closes
}

func (u *orderedUpstream) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	u.mtx.Lock()
	redial := u.conn != nil && u.redial != nil
	u.mtx.Unlock()
	if redial {
		<-u.redial
	}
	conn, err := (&websocket.Upgrader{}).Upgrade(rw, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	u.mtx.Lock()
	u.conn = conn
	u.requests = nil
	u.subscribed = map[string]bool{}
	u.mtx.Unlock()
	for {
		var req rpctypes.RPCRequest
		if err := conn.ReadJSON(&req); err != nil {
			return
		}
		var params struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(req.Params, &params)
		u.mtx.Lock()
		u.requests = append(u.requests, req.Method+" "+params.Query)
		switch req.Method {
		case "subscribe":
			u.subscribed[params.Query] = true
		case "unsubscribe":
			delete(u.subscribed, params.Query)
		}
		u.mtx.Unlock()
	}
}

func (u *orderedUpstream) state() ([]string, map[string]bool) {
	u.mtx.Lock()
	defer u.mtx.Unlock()
	s := map[string]bool{}
	for k, v := range u.subscribed {
		s[k] = v
	}
	return append([]string(nil), u.requests...), s
}

// Subscriber B joins query q while subscriber A, its last other user, leaves it.
// Afterwards B is registered locally, so the upstream must still be subscribed to q.
func TestConcurrentSubscribeAndLastUnsubscribeKeepsUpstream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := &orderedUpstream{}
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	w, err := newWsEvents(srv.URL)
	require.NoError(t, err)
	require.NoError(t, w.Start(ctx))
	t.Cleanup(func() { cancel(); _ = w.Stop() })

	_, err = w.Subscribe(ctx, "A", "q")
	require.NoError(t, err)
	require.Eventually(t, func() bool { _, s := upstream.state(); return s["q"] }, 5*time.Second, time.Millisecond)

	// While the writer is down, sends queue in FIFO order on the unbuffered channel.
	upstream.mtx.Lock()
	require.NoError(t, upstream.conn.Close())
	upstream.mtx.Unlock()
	require.Eventually(t, w.ws.IsReconnecting, 5*time.Second, time.Millisecond)

	bDone := make(chan error, 1)
	go func() {
		_, err := w.Subscribe(ctx, "B", "q") // blocks in the upstream send, not yet registered
		bDone <- err
	}()
	time.Sleep(100 * time.Millisecond)
	aDone := make(chan error, 1)
	go func() { aDone <- w.UnsubscribeAll(ctx, "A") }()

	require.NoError(t, <-bDone)
	require.NoError(t, <-aDone)

	w.mtx.RLock()
	sub, registered := w.subscriptions["q"]
	bRegistered := registered && sub.subscribers["B"] != nil
	w.mtx.RUnlock()
	require.True(t, bRegistered, "B is registered locally")

	// B's subscribe must reach this session; A's unsubscribe, if sent, may follow it.
	require.Eventually(t, func() bool { r, _ := upstream.state(); return slices.Contains(r, "subscribe q") },
		5*time.Second, time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	reqs, s := upstream.state()
	require.True(t, s["q"], "upstream unsubscribed q while B still needs it; requests on session: %v", reqs)
}

// forgetDuringResubscribe blocks a resubscribe of q on the stopped writer, then
// forgets q with a cleanup that times out before reaching the upstream. The
// reconnect waits for release; resubscribed closes once the resubscribe returns.
func forgetDuringResubscribe(ctx context.Context, t *testing.T) (w *wsEvents, upstream *orderedUpstream,
	resubscribed <-chan struct{}, release func()) {
	t.Helper()
	upstream = &orderedUpstream{redial: make(chan struct{})}
	w = startWsEvents(ctx, t, upstream)
	release = sync.OnceFunc(func() { close(upstream.redial) })
	t.Cleanup(release)
	_, err := w.Subscribe(ctx, "A", "q")
	require.NoError(t, err)
	require.Eventually(t, func() bool { _, s := upstream.state(); return s["q"] }, 5*time.Second, time.Millisecond)

	upstream.mtx.Lock()
	require.NoError(t, upstream.conn.Close())
	upstream.mtx.Unlock()
	require.Eventually(t, w.ws.IsReconnecting, 5*time.Second, time.Millisecond)
	sending := goroutinesIn("(*WSClient).Send")
	done := make(chan struct{})
	go func() { defer close(done); w.redoSubscriptionsAfter(ctx, 0) }()
	require.Eventually(t, func() bool { return goroutinesIn("(*WSClient).Send") == sending+1 },
		5*time.Second, time.Millisecond)

	cleanupCtx, cancelCleanup := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancelCleanup()
	require.ErrorIs(t, w.UnsubscribeAll(cleanupCtx, "A"), context.DeadlineExceeded)
	return w, upstream, done, release
}

// settleResubscribe waits for the resubscribe and the unsubscribes it started,
// then for a later request to reach the upstream, and returns the session state.
func settleResubscribe(ctx context.Context, t *testing.T, w *wsEvents, upstream *orderedUpstream,
	resubscribed <-chan struct{}) ([]string, map[string]bool) {
	t.Helper()
	<-resubscribed
	require.Eventually(t, func() bool { return goroutinesIn("(*wsEvents).unsubscribeOrphan") == 0 },
		5*time.Second, time.Millisecond)
	_, err := w.Subscribe(ctx, "S", "sync")
	require.NoError(t, err)
	require.Eventually(t, func() bool { _, s := upstream.state(); return s["sync"] }, 5*time.Second, time.Millisecond)
	return upstream.state()
}

// A query forgotten while its resubscribe was being sent must be unsubscribed
// afterwards, or it holds one of the node's per-client subscription slots.
func TestResubscribeOfForgottenQueryIsUnsubscribed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w, upstream, resubscribed, release := forgetDuringResubscribe(ctx, t)

	release()
	reqs, s := settleResubscribe(ctx, t, w, upstream, resubscribed)
	require.Contains(t, reqs, "subscribe q")
	require.False(t, s["q"], "the forgotten query stayed subscribed upstream; requests on session: %v", reqs)
}

// Joining a forgotten query during its resubscribe must keep the joiner's upstream
// subscription.
func TestResubscribeOfForgottenQueryKeepsRejoin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w, upstream, resubscribed, release := forgetDuringResubscribe(ctx, t)
	sending := goroutinesIn("(*WSClient).Send")
	joined := make(chan error, 1)
	go func() {
		_, err := w.Subscribe(ctx, "B", "q")
		joined <- err
	}()
	require.Eventually(t, func() bool { return goroutinesIn("(*WSClient).Send") == sending+1 },
		5*time.Second, time.Millisecond)

	release()
	require.NoError(t, <-joined)
	reqs, s := settleResubscribe(ctx, t, w, upstream, resubscribed)
	require.True(t, s["q"], "the rejoined query was unsubscribed upstream; requests on session: %v", reqs)
}
