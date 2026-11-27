package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/pubsub"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/rpc/coretypes"
	rpctypes "github.com/dashpay/tenderdash/rpc/jsonrpc/types"
	"github.com/dashpay/tenderdash/types"
)

// fakeUpstream replies to each subscribe with the events scripted for its query.
type fakeUpstream struct {
	mtx        sync.Mutex
	events     map[string][]string
	failed     map[string]bool          // queries whose subscribe gets an RPC error before their events
	gates      map[string]chan struct{} // hold a query's events until closed
	conn       *websocket.Conn
	subscribes []string
	others     []string // non-subscribe requests on any connection, as "method query"
	// unsubscribeErr, if set, is the reply to every unsubscribe.
	unsubscribeErr error
}

func (u *fakeUpstream) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{}
	conn, err := upgrader.Upgrade(rw, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	u.mtx.Lock()
	u.conn = conn
	u.subscribes = nil
	u.mtx.Unlock()
	for {
		var req rpctypes.RPCRequest
		if err := conn.ReadJSON(&req); err != nil {
			return
		}
		var params struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return
		}
		u.mtx.Lock()
		if req.Method != "subscribe" {
			u.others = append(u.others, strings.TrimSpace(req.Method+" "+params.Query))
			replyErr := u.unsubscribeErr
			u.mtx.Unlock()
			if req.Method == "unsubscribe" && replyErr != nil {
				if err := conn.WriteJSON(req.MakeError(replyErr)); err != nil {
					return
				}
			}
			continue
		}
		u.subscribes = append(u.subscribes, params.Query)
		events, failed, gate := u.events[params.Query], u.failed[params.Query], u.gates[params.Query]
		u.mtx.Unlock()
		if gate != nil {
			<-gate
		}
		if failed {
			if err := conn.WriteJSON(req.MakeErrorf(rpctypes.CodeInternalError, "subscribe failed")); err != nil {
				return
			}
		}
		for _, query := range events {
			res := req.MakeResponse(coretypes.ResultEvent{
				Query:          query,
				SubscriptionID: "id-" + query,
				Data:           types.EventDataNewRound{},
			})
			if err := conn.WriteJSON(res); err != nil {
				return
			}
		}
	}
}

// subscribe subscribes w to query and lets the upstream send the query's events
// only afterwards. wsEvents records a query after sending its request, so it drops
// events that arrive first.
func (u *fakeUpstream) subscribe(ctx context.Context, t *testing.T, w *wsEvents, query string) <-chan coretypes.ResultEvent {
	t.Helper()
	return u.subscribeAs(ctx, t, w, "", query)
}

func (u *fakeUpstream) subscribeAs(ctx context.Context, t *testing.T, w *wsEvents,
	subscriber, query string) <-chan coretypes.ResultEvent {
	t.Helper()
	gate := make(chan struct{})
	defer close(gate)
	u.mtx.Lock()
	if u.gates == nil {
		u.gates = make(map[string]chan struct{})
	}
	u.gates[query] = gate
	u.mtx.Unlock()
	out, err := w.Subscribe(ctx, subscriber, query)
	require.NoError(t, err)
	return out
}

func (u *fakeUpstream) setEvents(events map[string][]string) {
	u.mtx.Lock()
	defer u.mtx.Unlock()
	u.events = events
}

// dropConnection ends the session abruptly, which makes the client reconnect.
func (u *fakeUpstream) dropConnection(t *testing.T) {
	t.Helper()
	u.mtx.Lock()
	defer u.mtx.Unlock()
	require.NoError(t, u.conn.Close())
}

func (u *fakeUpstream) otherRequests() []string {
	u.mtx.Lock()
	defer u.mtx.Unlock()
	return append([]string(nil), u.others...)
}

func (u *fakeUpstream) subscribesOnCurrentConnection() []string {
	u.mtx.Lock()
	defer u.mtx.Unlock()
	return append([]string(nil), u.subscribes...)
}

func startWsEvents(ctx context.Context, t *testing.T, upstream http.Handler) *wsEvents {
	t.Helper()
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	w, err := newWsEvents(srv.URL)
	require.NoError(t, err)
	require.NoError(t, w.Start(ctx))
	// The test's deferred cancel has run by then, so no worker outlives its test.
	t.Cleanup(func() { requireStopReturns(t, w) })
	return w
}

// reconnecting drops the session and returns once the writer has stopped. The
// first redial happens at least a second later.
func reconnecting(t *testing.T, w *wsEvents, upstream *fakeUpstream) {
	t.Helper()
	upstream.dropConnection(t)
	require.Eventually(t, w.ws.IsReconnecting, 5*time.Second, time.Millisecond)
}

func requireEvent(t *testing.T, out <-chan coretypes.ResultEvent, query string) {
	t.Helper()
	select {
	case event := <-out:
		require.Equal(t, query, event.Query)
	case <-time.After(5 * time.Second):
		t.Fatalf("event for %q was not delivered", query)
	}
}

// A cleanup that times out during a reconnect must still forget the subscription,
// or the reconnect restores it and its unread channel stalls every other event.
func TestUnsubscribeAllTimeoutDuringReconnectForgetsSubscriptions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := &fakeUpstream{events: map[string][]string{
		"other": {"gone", "gone", "gone", "other"},
	}}
	w := startWsEvents(ctx, t, upstream)
	_, err := w.Subscribe(ctx, "", "gone")
	require.NoError(t, err)
	require.Eventually(t, func() bool { return len(upstream.subscribesOnCurrentConnection()) == 1 },
		5*time.Second, time.Millisecond)

	reconnecting(t, w, upstream)
	cleanupCtx, cancelCleanup := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancelCleanup()
	require.ErrorIs(t, w.UnsubscribeAll(cleanupCtx, ""), context.DeadlineExceeded)

	other := upstream.subscribe(ctx, t, w, "other")
	requireEvent(t, other, "other")
	require.NotContains(t, upstream.subscribesOnCurrentConnection(), "gone",
		"the reconnect restored a subscription whose cleanup timed out")
}

// One subscriber's cleanup, whether sent upstream or timed out during a reconnect,
// must leave every other subscriber's queries subscribed and delivering. All
// subscribers share one upstream session, so the cleanup may only unsubscribe
// queries nobody else uses.
func TestUnsubscribeAllKeepsOtherSubscribers(t *testing.T) {
	for name, tc := range map[string]struct {
		bQuery  string
		timeout bool
		sent    []string
	}{
		"sent":                     {bQuery: "b", sent: []string{"unsubscribe a"}},
		"timeout during reconnect": {bQuery: "b", timeout: true},
		"shared query":             {bQuery: "a"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			upstream := &fakeUpstream{}
			w := startWsEvents(ctx, t, upstream)
			a := upstream.subscribeAs(ctx, t, w, "A", "a")
			b := upstream.subscribeAs(ctx, t, w, "B", tc.bQuery)
			// Dropping a session with unread requests resets it, which the client misses.
			require.Eventually(t, func() bool { return len(upstream.subscribesOnCurrentConnection()) == 2 },
				5*time.Second, time.Millisecond)

			if tc.timeout {
				reconnecting(t, w, upstream)
				cleanupCtx, cancelCleanup := context.WithTimeout(ctx, 10*time.Millisecond)
				defer cancelCleanup()
				require.ErrorIs(t, w.UnsubscribeAll(cleanupCtx, "A"), context.DeadlineExceeded)
			} else {
				require.NoError(t, w.UnsubscribeAll(ctx, "A"))
				// The upstream handles requests in order, so this event follows the cleanup.
				upstream.setEvents(map[string][]string{"sync": {"sync"}})
				requireEvent(t, upstream.subscribeAs(ctx, t, w, "C", "sync"), "sync")
				require.Equal(t, tc.sent, upstream.otherRequests())
				reconnecting(t, w, upstream)
			}
			// Served only once the reconnect resubscribes.
			upstream.setEvents(map[string][]string{"a": {"a"}, "b": {"b"}})

			requireEvent(t, b, tc.bQuery)
			require.Empty(t, a, "a cleaned up subscriber received an event")
			if tc.bQuery != "a" {
				require.NotContains(t, upstream.subscribesOnCurrentConnection(), "a",
					"the reconnect restored a cleaned up subscriber's query")
			}
		})
	}
}

// A cleanup timing out behind another subscriber's send during a reconnect must
// still forget its subscriptions, or the reconnect restores an unread channel.
func TestUnsubscribeAllTimeoutBehindSubscribeForgetsSubscriptions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := &fakeUpstream{}
	w := startWsEvents(ctx, t, upstream)
	upstream.subscribeAs(ctx, t, w, "A", "gone")
	require.Eventually(t, func() bool { return len(upstream.subscribesOnCurrentConnection()) == 1 },
		5*time.Second, time.Millisecond)

	reconnecting(t, w, upstream)
	subscribed := make(chan error, 1)
	go func() {
		_, err := w.Subscribe(ctx, "B", "other")
		subscribed <- err
	}()
	require.Eventually(t, func() bool { return len(w.changes) == 1 }, 5*time.Second, time.Millisecond)
	cleanupCtx, cancelCleanup := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancelCleanup()
	require.ErrorIs(t, w.UnsubscribeAll(cleanupCtx, "A"), context.DeadlineExceeded)

	require.NoError(t, <-subscribed)
	upstream.setEvents(map[string][]string{"sync": {"sync"}})
	requireEvent(t, upstream.subscribeAs(ctx, t, w, "C", "sync"), "sync")
	require.NotContains(t, upstream.subscribesOnCurrentConnection(), "gone",
		"the reconnect restored a subscription whose cleanup timed out")
}

// Forgetting a subscription must also release a delivery blocked on its unread channel.
func TestUnsubscribeAllReleasesBlockedDelivery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := &fakeUpstream{events: map[string][]string{
		"gone":  {"gone", "gone", "gone"},
		"other": {"other"},
	}}
	w := startWsEvents(ctx, t, upstream)
	gone := upstream.subscribe(ctx, t, w, "gone")
	// Taking one event lets the next fill the buffer, so the third has to block.
	requireEvent(t, gone, "gone")
	require.Eventually(t, func() bool { return len(gone) == 1 }, 5*time.Second, time.Millisecond)
	// Nothing observable marks the delivery blocking; this window only lets it get
	// there, and cannot fail correct code.
	<-time.After(50 * time.Millisecond)

	require.NoError(t, w.UnsubscribeAll(ctx, ""))
	other := upstream.subscribe(ctx, t, w, "other")
	requireEvent(t, other, "other")
}

// UnsubscribeAll returns without waiting for the resubscribe's in-flight send.
// The resubscribe skips the other forgotten queries and asynchronously unsubscribes
// the in-flight query after its send completes. Events for forgotten queries are dropped.
func TestUnsubscribeAllDuringResubscribe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := &fakeUpstream{}
	w := startWsEvents(ctx, t, upstream)
	for _, query := range []string{"a EXISTS", "b EXISTS"} {
		_, err := w.Subscribe(ctx, "", query)
		require.NoError(t, err)
	}
	require.Eventually(t, func() bool { return len(upstream.subscribesOnCurrentConnection()) == 2 },
		5*time.Second, time.Millisecond)
	// Only after the reconnect: unread events would stop the client noticing the drop.
	upstream.setEvents(map[string][]string{
		"a EXISTS": {"a", "a", "a"},
		"b EXISTS": {"b", "b", "b"},
		"other":    {"other"},
	})

	reconnecting(t, w, upstream)
	resubscribed := make(chan struct{})
	go func() { defer close(resubscribed); w.redoSubscriptionsAfter(ctx, 0) }()
	// Nothing observable marks the resubscribe blocking on the stopped writer; this
	// window only lets it get there, and cannot fail correct code.
	<-time.After(50 * time.Millisecond)

	cleanupCtx, cancelCleanup := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancelCleanup()
	unsubscribed := make(chan error, 1)
	go func() { unsubscribed <- w.UnsubscribeAll(cleanupCtx, "") }()
	select {
	case err := <-unsubscribed:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-resubscribed:
		t.Fatal("UnsubscribeAll waited for the resubscribe to finish")
	}

	select {
	case <-resubscribed:
	case <-time.After(10 * time.Second):
		t.Fatal("resubscribe did not finish after the reconnect")
	}
	other := upstream.subscribe(ctx, t, w, "other")
	requireEvent(t, other, "other")
	restored := 0
	for _, query := range upstream.subscribesOnCurrentConnection() {
		if query != "other" {
			restored++
		}
	}
	require.LessOrEqual(t, restored, 1, "the resubscribe restored more than the query it was already sending")
}

// A failed resubscribe forgets only the subscription it sent for: one made for
// the same query during the send has its own upstream subscription.
func TestFailedResubscribeKeepsReplacedSubscription(t *testing.T) {
	w, err := newWsEvents("tcp://127.0.0.1:1")
	require.NoError(t, err)
	w.subscriptions["q"] = &wsSubscription{query: "q", subscribers: map[string]*wsSubscriber{}}
	sending := goroutinesIn("(*WSClient).Send")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resubscribed := make(chan struct{})
	// The client never started, so the send blocks until ctx ends.
	go func() { defer close(resubscribed); w.redoSubscriptionsAfter(ctx, 0) }()
	require.Eventually(t, func() bool { return goroutinesIn("(*WSClient).Send") == sending+1 },
		5*time.Second, time.Millisecond)

	replacement := &wsSubscription{query: "q", subscribers: map[string]*wsSubscriber{}}
	w.mtx.Lock()
	w.subscriptions["q"] = replacement
	w.mtx.Unlock()
	cancel()
	<-resubscribed

	w.mtx.RLock()
	defer w.mtx.RUnlock()
	require.Same(t, replacement, w.subscriptions["q"])
}

// A leave and the cleanup of a query forgotten during its resubscribe can both
// unsubscribe it. The second gets "subscription not found", which loses nothing,
// so it must not queue a resubscribe.
func TestSubscriptionNotFoundDoesNotResubscribe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := &fakeUpstream{
		events:         map[string][]string{"q": {"q"}},
		failed:         map[string]bool{"boom": true},
		unsubscribeErr: pubsub.ErrSubscriptionNotFound,
	}
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	w, err := newWsEvents(srv.URL)
	require.NoError(t, err)
	var logs bytes.Buffer
	w.Logger, err = log.NewLogger(log.LogLevelDebug, &logs)
	require.NoError(t, err)
	w.resubscribeDelay = time.Hour // only cancellation can end this delay
	require.NoError(t, w.Start(ctx))
	t.Cleanup(func() { requireStopReturns(t, w) })

	_, err = w.Subscribe(ctx, "", "a")
	require.NoError(t, err)

	// Occupy the resubscriber so it cannot consume another queued retry.
	require.NoError(t, w.ws.Subscribe(ctx, "boom"))
	require.Eventually(t, func() bool { return goroutinesIn("(*wsEvents).redoSubscriptionsAfter") == 1 },
		5*time.Second, time.Millisecond)

	require.NoError(t, w.Unsubscribe(ctx, "", "a"))
	// The upstream handles requests in order, so this event follows the error.
	requireEvent(t, upstream.subscribe(ctx, t, w, "q"), "q")
	require.Len(t, w.resubscribe, 0, "subscription not found queued a resubscribe")
	cancel()
	requireStopReturns(t, w)
	require.Contains(t, logs.String(), `"message":"ignoring harmless WS subscription error"`)
}

// Every UnsubscribeAll gets a fresh signal: repeating it must not panic, and a
// later subscription must still receive all events through its small buffer.
func TestSubscribeAfterRepeatedUnsubscribeAllBlocksNormally(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make([]string, 16)
	for i := range events {
		events[i] = "later"
	}
	upstream := &fakeUpstream{events: map[string][]string{"later": events}}
	w := startWsEvents(ctx, t, upstream)
	require.NoError(t, w.UnsubscribeAll(ctx, ""))
	require.NoError(t, w.UnsubscribeAll(ctx, ""))

	later := upstream.subscribe(ctx, t, w, "later")
	for range events {
		require.Eventually(t, func() bool { return len(later) == 1 }, 5*time.Second, time.Millisecond)
		requireEvent(t, later, "later")
	}
}

// goroutinesIn counts running goroutines with a frame in the named function.
func goroutinesIn(function string) int {
	stacks := make([]byte, 1<<24)
	return strings.Count(string(stacks[:runtime.Stack(stacks, true)]), function+"(")
}

// requireListenerExited must hold as soon as the join returns, so it does not wait.
func requireListenerExited(t *testing.T, w *wsEvents) {
	t.Helper()
	select {
	case <-w.listenerDone:
	default:
		t.Fatal("eventListener outlived the join")
	}
}

func requireStopReturns(t *testing.T, w *wsEvents) {
	t.Helper()
	stopped := make(chan error, 1)
	go func() { stopped <- w.Stop() }()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return")
	}
}

// An upstream RPC error sends the resubscriber into a delayed resubscribe. Ending
// the context there must end it, and Stop must join it with eventListener.
func TestStopJoinsListenerDuringResubscribe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := httptest.NewServer(&fakeUpstream{failed: map[string]bool{"q": true}})
	t.Cleanup(srv.Close)
	w, err := newWsEvents(srv.URL)
	require.NoError(t, err)
	w.resubscribeDelay = time.Hour // only cancellation can end this delay
	require.NoError(t, w.Start(ctx))
	t.Cleanup(func() { requireStopReturns(t, w) })
	_, err = w.Subscribe(ctx, "", "q")
	require.NoError(t, err)
	require.Eventually(t, func() bool { return goroutinesIn("(*wsEvents).redoSubscriptionsAfter") == 1 },
		5*time.Second, time.Millisecond)

	cancel()
	requireStopReturns(t, w)
	requireListenerExited(t, w)
}

// A subscriber that stopped reading must not turn the join into a shutdown hang.
func TestStopJoinsListenerBlockedOnSubscriber(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := &fakeUpstream{events: map[string][]string{"q": {"q", "q", "q"}}}
	w := startWsEvents(ctx, t, upstream)
	q := upstream.subscribe(ctx, t, w, "q")
	require.Eventually(t, func() bool { return len(q) == 1 }, 5*time.Second, time.Millisecond)

	cancel()
	requireStopReturns(t, w)
	requireListenerExited(t, w)
}

// After the client shuts down by itself the context can still be live, and then
// only Stop can release a delivery no subscriber is reading.
func TestStopListenerReleasesDeliveryWithLiveContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := &fakeUpstream{events: map[string][]string{"q": {"q", "q", "q"}}}
	w := startWsEvents(ctx, t, upstream)
	q := upstream.subscribe(ctx, t, w, "q")
	require.Eventually(t, func() bool { return len(q) == 1 }, 5*time.Second, time.Millisecond)

	stopped := make(chan struct{})
	go func() { w.stopListener(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stopListener blocked on a subscriber not reading")
	}
	requireListenerExited(t, w)
}

// Starting fails cleanly, and a client that never ran has nothing to join.
func TestStopWithoutRunningListener(t *testing.T) {
	w, err := newWsEvents("tcp://127.0.0.1:1")
	require.NoError(t, err)
	require.Error(t, w.Start(context.Background()))
	requireStopReturns(t, w)
}

// Stop must not report completion while the event loop can still run, here held
// on a lock that does not observe cancellation.
func TestStopWaitsForListenerToExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := &fakeUpstream{events: map[string][]string{"q": {"q"}}}
	w := startWsEvents(ctx, t, upstream)
	w.mtx.Lock()
	unlock := sync.OnceFunc(w.mtx.Unlock)
	defer unlock()
	require.NoError(t, w.ws.Subscribe(ctx, "q"))
	require.Eventually(t, func() bool {
		stacks := make([]byte, 1<<24)
		for _, stack := range strings.Split(string(stacks[:runtime.Stack(stacks, true)]), "\n\n") {
			if strings.Contains(stack, "(*wsEvents).eventListener(") && strings.Contains(stack, "RLock") {
				return true
			}
		}
		return false
	}, 5*time.Second, time.Millisecond)

	cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- w.Stop() }()
	// Nothing observable marks Stop blocking; this window cannot fail correct code.
	select {
	case err := <-stopped:
		t.Fatalf("Stop returned while the event loop was still running: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after the event loop could exit")
	}
}
