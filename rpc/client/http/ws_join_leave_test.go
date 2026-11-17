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
}

func (u *orderedUpstream) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
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
