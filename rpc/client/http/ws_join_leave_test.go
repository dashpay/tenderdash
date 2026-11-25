package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	tmquery "github.com/dashpay/tenderdash/internal/pubsub/query"
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
	canonical  bool
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
		key := params.Query
		valid := true
		if u.canonical {
			q, err := tmquery.New(key)
			valid = err == nil
			if valid {
				key = q.String()
			}
		}
		switch req.Method {
		case "subscribe":
			if valid {
				u.subscribed[key] = true
			}
		case "unsubscribe":
			if valid {
				delete(u.subscribed, key)
			} else {
				// A malformed unsubscribe is treated as an ID and removes all queries.
				clear(u.subscribed)
			}
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
func forgetDuringResubscribe(ctx context.Context, t *testing.T, query, replacement string) (w *wsEvents, upstream *orderedUpstream,
	resubscribed <-chan struct{}, release func()) {
	t.Helper()
	upstream = &orderedUpstream{redial: make(chan struct{}), canonical: true}
	w = startWsEvents(ctx, t, upstream)
	release = sync.OnceFunc(func() { close(upstream.redial) })
	t.Cleanup(release)
	_, err := w.Subscribe(ctx, "A", query)
	require.NoError(t, err)
	require.Eventually(t, func() bool { r, _ := upstream.state(); return slices.Contains(r, "subscribe "+query) }, 5*time.Second, time.Millisecond)

	upstream.mtx.Lock()
	require.NoError(t, upstream.conn.Close())
	upstream.mtx.Unlock()
	require.Eventually(t, w.ws.IsReconnecting, 5*time.Second, time.Millisecond)
	if replacement != "" {
		// Queue the live stream first so the upstream accepts it before the resend.
		sending := goroutinesIn("(*WSClient).Send")
		joined := make(chan error, 1)
		go func() {
			_, err := w.Subscribe(ctx, "B", replacement)
			joined <- err
		}()
		require.Eventually(t, func() bool { return goroutinesIn("(*WSClient).Send") == sending+1 },
			5*time.Second, time.Millisecond)
		resume := release
		release = func() {
			resume()
			select {
			case err := <-joined:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("replacement subscribe did not return")
			}
		}
	}
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
	select {
	case <-resubscribed:
	case <-time.After(5 * time.Second):
		t.Fatal("resubscribe did not return")
	}
	settled := make(chan struct{})
	go func() { w.orphans.Wait(); close(settled) }()
	select {
	case <-settled:
	case <-time.After(5 * time.Second):
		t.Fatal("orphan cleanup did not return")
	}
	_, err := w.Subscribe(ctx, "S", "sync.event EXISTS")
	require.NoError(t, err)
	require.Eventually(t, func() bool { _, s := upstream.state(); return s["sync.event EXISTS"] }, 5*time.Second, time.Millisecond)
	return upstream.state()
}

// A query forgotten while its resubscribe was being sent must be unsubscribed
// afterwards, or it holds one of the node's per-client subscription slots.
func TestResubscribeOfForgottenQueryIsUnsubscribed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w, upstream, resubscribed, release := forgetDuringResubscribe(ctx, t, "tm.event = 'NewBlock'", "")

	release()
	reqs, s := settleResubscribe(ctx, t, w, upstream, resubscribed)
	require.Contains(t, reqs, "subscribe tm.event = 'NewBlock'")
	require.False(t, s["tm.event = 'NewBlock'"], "the forgotten query stayed subscribed upstream; requests on session: %v", reqs)
}

// Joining a forgotten query during its resubscribe must keep the joiner's upstream
// subscription.
func TestResubscribeOfForgottenQueryKeepsRejoin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w, upstream, resubscribed, release := forgetDuringResubscribe(ctx, t, "tm.event = 'NewBlock'", "")
	sending := goroutinesIn("(*WSClient).Send")
	joined := make(chan error, 1)
	go func() {
		_, err := w.Subscribe(ctx, "B", "tm.event = 'NewBlock'")
		joined <- err
	}()
	require.Eventually(t, func() bool { return goroutinesIn("(*WSClient).Send") == sending+1 },
		5*time.Second, time.Millisecond)

	release()
	require.NoError(t, <-joined)
	reqs, s := settleResubscribe(ctx, t, w, upstream, resubscribed)
	require.True(t, s["tm.event = 'NewBlock'"], "the rejoined query was unsubscribed upstream; requests on session: %v", reqs)
}

func TestStopWaitsForOrphanCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := startWsEvents(ctx, t, &orderedUpstream{})
	// Hold the orphan's subscription check across cancellation.
	w.mtx.Lock()
	unlock := sync.OnceFunc(w.mtx.Unlock)
	defer unlock()
	w.orphans.Add(1)
	go w.unsubscribeOrphan(w.lifetime, "tm.event = 'NewBlock'")
	require.Eventually(t, func() bool { return len(w.changes) == 1 }, 5*time.Second, time.Millisecond)

	cancel()
	stopped := make(chan struct{})
	var stopErr error
	go func() {
		defer close(stopped)
		stopErr = w.Stop()
	}()
	require.Eventually(t, func() bool {
		select {
		case <-stopped:
			return true
		default:
		}
		stacks := make([]byte, 1<<24)
		for _, stack := range strings.Split(string(stacks[:runtime.Stack(stacks, true)]), "\n\n") {
			if strings.Contains(stack, "(*wsEvents).Stop(") && strings.Contains(stack, "(*WaitGroup).Wait(") &&
				!strings.Contains(stack, "(*WSClient).Stop(") {
				return true
			}
		}
		return false
	}, 5*time.Second, time.Millisecond)
	select {
	case <-stopped:
		t.Fatal("Stop returned while orphan cleanup was still blocked")
	default:
	}

	unlock()
	select {
	case <-stopped:
		require.NoError(t, stopErr)
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after orphan cleanup was released")
	}
}

func TestOrphanCleanupPreservesLiveQuery(t *testing.T) {
	for _, tc := range []struct {
		name      string
		forgotten string
	}{
		{name: "canonical_rejoin", forgotten: "tm.event='NewBlock'"},
		{name: "malformed_query", forgotten: "tm.event ="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			const live = "tm.event = 'NewBlock'"
			w, upstream, resubscribed, release := forgetDuringResubscribe(ctx, t, tc.forgotten, live)
			release()
			reqs, state := settleResubscribe(ctx, t, w, upstream, resubscribed)
			require.GreaterOrEqual(t, len(reqs), 2)
			require.Equal(t, "subscribe "+live, reqs[0])
			require.Equal(t, "subscribe "+tc.forgotten, reqs[1])
			w.mtx.RLock()
			owned := w.subscriptions[live] != nil && w.subscriptions[live].subscribers["B"] != nil
			w.mtx.RUnlock()
			require.True(t, owned, "the live query must still have a local owner")
			require.True(t, state[live], "the live stream was removed; requests: %v", reqs)
			require.NotContains(t, reqs, "unsubscribe "+tc.forgotten)
		})
	}
}
