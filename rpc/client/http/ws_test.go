package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/rpc/coretypes"
	rpctypes "github.com/dashpay/tenderdash/rpc/jsonrpc/types"
	"github.com/dashpay/tenderdash/types"
)

// fakeUpstream replies to each subscribe with the events scripted for its query.
type fakeUpstream struct {
	mtx        sync.Mutex
	events     map[string][]string
	conn       *websocket.Conn
	subscribes []string
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
		if req.Method != "subscribe" {
			continue
		}
		var params struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return
		}
		u.mtx.Lock()
		u.subscribes = append(u.subscribes, params.Query)
		events := u.events[params.Query]
		u.mtx.Unlock()
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

func (u *fakeUpstream) subscribesOnCurrentConnection() []string {
	u.mtx.Lock()
	defer u.mtx.Unlock()
	return append([]string(nil), u.subscribes...)
}

func startWsEvents(ctx context.Context, t *testing.T, upstream *fakeUpstream) *wsEvents {
	t.Helper()
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	w, err := newWsEvents(srv.URL)
	require.NoError(t, err)
	require.NoError(t, w.Start(ctx))
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

	other, err := w.Subscribe(ctx, "", "other")
	require.NoError(t, err)
	requireEvent(t, other, "other")
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
	gone, err := w.Subscribe(ctx, "", "gone")
	require.NoError(t, err)
	// Taking one event lets the next fill the buffer, so the third has to block.
	requireEvent(t, gone, "gone")
	require.Eventually(t, func() bool { return len(gone) == 1 }, 5*time.Second, time.Millisecond)

	require.NoError(t, w.UnsubscribeAll(ctx, ""))
	other, err := w.Subscribe(ctx, "", "other")
	require.NoError(t, err)
	requireEvent(t, other, "other")
}

// A clear landing during a resubscribe must not wait for it, and stops it restoring
// more than the one query already being sent. That one stays subscribed upstream,
// but only until the next reconnect, and its events are dropped.
func TestUnsubscribeAllDuringResubscribe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := &fakeUpstream{}
	w := startWsEvents(ctx, t, upstream)
	for _, query := range []string{"a", "b"} {
		_, err := w.Subscribe(ctx, "", query)
		require.NoError(t, err)
	}
	require.Eventually(t, func() bool { return len(upstream.subscribesOnCurrentConnection()) == 2 },
		5*time.Second, time.Millisecond)
	// Only after the reconnect: unread events would stop the client noticing the drop.
	upstream.setEvents(map[string][]string{
		"a":     {"a", "a", "a"},
		"b":     {"b", "b", "b"},
		"other": {"other"},
	})

	reconnecting(t, w, upstream)
	resubscribed := make(chan struct{})
	go func() { defer close(resubscribed); w.redoSubscriptionsAfter(0) }()
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
	other, err := w.Subscribe(ctx, "", "other")
	require.NoError(t, err)
	requireEvent(t, other, "other")
	restored := 0
	for _, query := range upstream.subscribesOnCurrentConnection() {
		if query != "other" {
			restored++
		}
	}
	require.LessOrEqual(t, restored, 1, "the resubscribe restored more than the query it was already sending")
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

	later, err := w.Subscribe(ctx, "", "later")
	require.NoError(t, err)
	for range events {
		require.Eventually(t, func() bool { return len(later) == 1 }, 5*time.Second, time.Millisecond)
		requireEvent(t, later, "later")
	}
}
