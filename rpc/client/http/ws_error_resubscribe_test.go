package http

import (
	"context"
	"errors"
	"net"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// holdingConn, once armed, holds each write until released, then fails it.
type holdingConn struct {
	net.Conn
	mtx     sync.Mutex
	held    chan struct{}
	release chan struct{}
}

func (c *holdingConn) arm() (held <-chan struct{}, release func()) {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	c.held, c.release = make(chan struct{}, 1), make(chan struct{})
	return c.held, sync.OnceFunc(func() { close(c.release) })
}

func (c *holdingConn) Write(p []byte) (int, error) {
	c.mtx.Lock()
	held, release := c.held, c.release
	c.mtx.Unlock()
	if held == nil {
		return c.Conn.Write(p)
	}
	select {
	case held <- struct{}{}:
	default:
	}
	<-release
	return 0, errors.New("write failed")
}

// readerDelivering reports whether the WSClient reader waits for the event loop
// to take a response.
func readerDelivering() bool {
	stacks := make([]byte, 1<<24)
	for _, stack := range strings.Split(string(stacks[:runtime.Stack(stacks, true)]), "\n\n") {
		header, _, _ := strings.Cut(stack, "\n")
		if strings.Contains(header, "[select") && strings.Contains(stack, "(*WSClient).readRoutine(") {
			return true
		}
	}
	return false
}

// An RPC error starts a resubscribe that can wait for the writer, which restarts
// only once the reader hands its response to the event loop. The event loop must
// keep taking responses meanwhile, or the client stalls until it shuts down.
func TestErrorResubscribeKeepsDeliveringEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gate := make(chan struct{})
	upstream := &fakeUpstream{
		events: map[string][]string{"boom": {"q1"}},
		failed: map[string]bool{"boom": true},
		gates:  map[string]chan struct{}{"boom": gate},
	}
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	w, err := newWsEvents(srv.URL)
	require.NoError(t, err)
	w.resubscribeDelay = 0
	var conn atomic.Pointer[holdingConn]
	dial := w.ws.DialerContext
	w.ws.DialerContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		hc := &holdingConn{Conn: c}
		conn.CompareAndSwap(nil, hc)
		return hc, nil
	}
	require.NoError(t, w.Start(ctx))
	t.Cleanup(func() { requireStopReturns(t, w) })
	q1 := upstream.subscribe(ctx, t, w, "q1")
	upstream.subscribe(ctx, t, w, "q2")

	// The upstream replies to boom with an error, then an event for q1.
	require.NoError(t, w.ws.Subscribe(ctx, "boom"))
	require.Eventually(t, func() bool { return len(upstream.subscribesOnCurrentConnection()) == 3 },
		5*time.Second, time.Millisecond)
	held, release := conn.Load().arm()
	t.Cleanup(release)
	close(gate)

	// The resubscribe's first write fails once the reader holds the event, which
	// stops the writer before the next resubscribe.
	<-held
	require.Eventually(t, func() bool { return len(q1) == 1 || readerDelivering() }, 5*time.Second, time.Millisecond)
	release()
	requireEvent(t, q1, "q1")
}

// Errors during a pending resubscribe coalesce into one more, and events keep
// flowing during its delay.
func TestRepeatedErrorsCoalesceResubscribes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := &fakeUpstream{
		events: map[string][]string{"boom": {"q"}},
		failed: map[string]bool{"boom": true},
	}
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	w, err := newWsEvents(srv.URL)
	require.NoError(t, err)
	w.resubscribeDelay = time.Hour // only cancellation can end this delay
	require.NoError(t, w.Start(ctx))
	t.Cleanup(func() { requireStopReturns(t, w) })
	q := upstream.subscribe(ctx, t, w, "q")

	for i := 0; i < 3; i++ {
		require.NoError(t, w.ws.Subscribe(ctx, "boom"))
		// The event follows the error, so the event loop has taken it.
		requireEvent(t, q, "q")
		if i == 0 {
			require.Eventually(t, func() bool { return goroutinesIn("(*wsEvents).redoSubscriptionsAfter") == 1 },
				5*time.Second, time.Millisecond)
		}
	}
	require.Equal(t, 1, goroutinesIn("(*wsEvents).redoSubscriptionsAfter"))
	require.Len(t, w.resubscribe, 1)
}
