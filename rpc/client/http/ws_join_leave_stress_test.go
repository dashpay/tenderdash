package http

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Concurrent joins and leaves of one query must leave the upstream subscribed
// exactly when some local subscriber remains.
func TestJoinLeaveStressKeepsUpstreamConsistent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := &orderedUpstream{}
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	w, err := newWsEvents(srv.URL)
	require.NoError(t, err)
	require.NoError(t, w.Start(ctx))
	t.Cleanup(func() { cancel(); _ = w.Stop() })

	for round := 0; round < 300; round++ {
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(2)
			name := fmt.Sprintf("s%d-%d", round, i)
			go func() {
				defer wg.Done()
				_, err := w.Subscribe(ctx, name, "q", 100)
				require.NoError(t, err)
			}()
			go func(prev string) {
				defer wg.Done()
				require.NoError(t, w.UnsubscribeAll(ctx, prev))
			}(fmt.Sprintf("s%d-%d", round-1, i))
		}
		wg.Wait()
		// Settle: a sync request on a separate query follows every send above.
		_, err := w.Subscribe(ctx, "sync", fmt.Sprintf("sync%d", round))
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			_, s := upstream.state()
			return s[fmt.Sprintf("sync%d", round)]
		}, 5*time.Second, time.Millisecond)
		w.mtx.Lock()
		sub, local := w.subscriptions["q"]
		n := 0
		if local {
			n = len(sub.subscribers)
		}
		w.mtx.Unlock()
		reqs, s := upstream.state()
		if local != s["q"] {
			t.Fatalf("round %d: local q=%v (%d subscribers) upstream q=%v; last requests %v",
				round, local, n, s["q"], reqs[max(0, len(reqs)-12):])
		}
	}
}

// Shutdown while a Subscribe holds the changes lock blocked in its send, and a
// cleanup without deadline waits for that lock, must release both and Stop.
func TestStopWhileSubscribeHoldsChangesLock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := &fakeUpstream{}
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	w, err := newWsEvents(srv.URL)
	require.NoError(t, err)
	require.NoError(t, w.Start(ctx))
	upstream.subscribeAs(ctx, t, w, "A", "gone")
	require.Eventually(t, func() bool { return len(upstream.subscribesOnCurrentConnection()) == 1 },
		5*time.Second, time.Millisecond)
	reconnecting(t, w, upstream)
	srv.Close() // redials keep failing

	subscribed := make(chan error, 1)
	go func() { _, err := w.Subscribe(context.Background(), "B", "other"); subscribed <- err }()
	require.Eventually(t, func() bool { return len(w.changes) == 1 }, 5*time.Second, time.Millisecond)
	cleaned := make(chan error, 1)
	go func() { cleaned <- w.UnsubscribeAll(context.Background(), "A") }()
	time.Sleep(50 * time.Millisecond)

	cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- w.Stop() }()
	for name, ch := range map[string]chan error{"Stop": stopped, "Subscribe": subscribed, "UnsubscribeAll": cleaned} {
		select {
		case err := <-ch:
			t.Logf("%s returned %v", name, err)
		case <-time.After(10 * time.Second):
			t.Fatalf("%s hung after shutdown", name)
		}
	}
	require.Empty(t, w.changes, "changes lock leaked")
}
