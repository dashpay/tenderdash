package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
	rpcmock "github.com/dashpay/tenderdash/rpc/client/mocks"
	"github.com/dashpay/tenderdash/rpc/coretypes"
	rpctypes "github.com/dashpay/tenderdash/rpc/jsonrpc/types"
)

const (
	queryNewBlock = "tm.event = 'NewBlock'"
	queryTx       = "tm.event = 'Tx'"
)

// fakeWSSession is a WebSocket session that stays open for the whole test.
type fakeWSSession struct {
	ctx     context.Context
	remote  string
	writes  chan rpctypes.RPCResponse
	retired []chan struct{} // one per Go call, closed once its worker returns
}

func newFakeWSSession(ctx context.Context, remote string) *fakeWSSession {
	return &fakeWSSession{ctx: ctx, remote: remote, writes: make(chan rpctypes.RPCResponse, 16)}
}

func (s *fakeWSSession) Go(f func(context.Context)) bool {
	retired := make(chan struct{})
	s.retired = append(s.retired, retired)
	go func() { defer close(retired); f(s.ctx) }()
	return true
}

func (s *fakeWSSession) GetRemoteAddr() string { return s.remote }

func (s *fakeWSSession) WriteRPCResponse(_ context.Context, resp rpctypes.RPCResponse) error {
	s.writes <- resp
	return nil
}

func (s *fakeWSSession) TryWriteRPCResponse(_ context.Context, resp rpctypes.RPCResponse) bool {
	s.writes <- resp
	return true
}

func (s *fakeWSSession) Context() context.Context { return s.ctx }

func (s *fakeWSSession) callCtx(id int) context.Context {
	req := rpctypes.NewRequest(id)
	return rpctypes.WithCallInfo(s.ctx, &rpctypes.CallInfo{WSConn: s, RPCRequest: &req})
}

type wsSubscription struct {
	events  chan<- coretypes.ResultEvent
	retired <-chan struct{} // closed once the session worker, which joins the forwarder, returns
	id      string
}

func startWSClient(t *testing.T) (*Client, *rpcmock.Client, context.Context) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	next := rpcmock.NewClient(t)
	next.On("Start", mock.Anything).Return(nil)
	c := NewClient(log.NewNopLogger(), next, nil)
	require.NoError(t, c.Start(ctx))
	t.Cleanup(func() { cancel(); c.Wait() })
	return c, next, ctx
}

func subscribeWS(t *testing.T, c *Client, next *rpcmock.Client, s *fakeWSSession, id int, query string) wsSubscription {
	t.Helper()
	events := make(chan coretypes.ResultEvent)
	next.On("Subscribe", mock.Anything, s.remote, query).
		Return((<-chan coretypes.ResultEvent)(events), nil).Once()
	_, err := c.SubscribeWS(s.callCtx(id), query)
	require.NoError(t, err)
	return wsSubscription{events: events, retired: s.retired[len(s.retired)-1], id: rpctypes.NewRequest(id).ID()}
}

func requireRetired(t *testing.T, sub wsSubscription) {
	t.Helper()
	select {
	case <-sub.retired:
	case <-time.After(5 * time.Second):
		t.Fatalf("workers of subscription %s still running after it was removed", sub.id)
	}
}

// requireForwards checks that sub's forwarder still relays events to s.
func requireForwards(t *testing.T, s *fakeWSSession, sub wsSubscription) {
	t.Helper()
	select {
	case sub.events <- coretypes.ResultEvent{}:
	case <-sub.retired:
		t.Fatalf("subscription %s retired while still subscribed", sub.id)
	case <-time.After(5 * time.Second):
		t.Fatalf("subscription %s stopped reading events", sub.id)
	}
	select {
	case resp := <-s.writes:
		require.Equal(t, sub.id, resp.ID())
	case <-time.After(5 * time.Second):
		t.Fatalf("subscription %s did not forward its event", sub.id)
	}
}

// requireTracked checks that the registry holds exactly the live forwarders;
// call it once the retired ones have exited.
func requireTracked(t *testing.T, c *Client, live int) {
	t.Helper()
	c.wsMtx.Lock()
	defer c.wsMtx.Unlock()
	require.Len(t, c.wsForwarders, live)
}

// The backend drops a subscriber it replaces without closing its channel, so
// the replaced forwarder must be retired by the light client.
func TestSubscribeWSReplacementRetiresWorkers(t *testing.T) {
	c, next, ctx := startWSClient(t)
	s := newFakeWSSession(ctx, "peer:1")

	replaced := subscribeWS(t, c, next, s, 1, queryNewBlock)
	current := subscribeWS(t, c, next, s, 2, queryNewBlock)

	requireRetired(t, replaced)
	requireTracked(t, c, 1)
	requireForwards(t, s, current)

	// The replaced forwarder's exit must not have untracked its replacement.
	next.On("Unsubscribe", mock.Anything, s.remote, queryNewBlock).Return(nil).Once()
	_, err := c.UnsubscribeWS(s.callCtx(3), queryNewBlock)
	require.NoError(t, err)
	requireRetired(t, current)
	requireTracked(t, c, 0)
}

func TestUnsubscribeWSRetiresWorkers(t *testing.T) {
	c, next, ctx := startWSClient(t)
	s := newFakeWSSession(ctx, "peer:1")
	removed := subscribeWS(t, c, next, s, 1, queryNewBlock)
	kept := subscribeWS(t, c, next, s, 2, queryTx)
	next.On("Unsubscribe", mock.Anything, s.remote, queryNewBlock).Return(nil).Once()

	_, err := c.UnsubscribeWS(s.callCtx(3), queryNewBlock)
	require.NoError(t, err)

	requireRetired(t, removed)
	requireTracked(t, c, 1)
	requireForwards(t, s, kept)
}

func TestUnsubscribeAllWSRetiresWorkers(t *testing.T) {
	c, next, ctx := startWSClient(t)
	s := newFakeWSSession(ctx, "peer:1")
	other := newFakeWSSession(ctx, "peer:2")
	removed := []wsSubscription{
		subscribeWS(t, c, next, s, 1, queryNewBlock),
		subscribeWS(t, c, next, s, 2, queryTx),
	}
	otherSub := subscribeWS(t, c, next, other, 3, queryNewBlock)
	next.On("UnsubscribeAll", mock.Anything, s.remote).Return(nil).Once()

	_, err := c.UnsubscribeAllWS(s.callCtx(4))
	require.NoError(t, err)

	for _, sub := range removed {
		requireRetired(t, sub)
	}
	requireTracked(t, c, 1)
	requireForwards(t, other, otherSub)
	resubscribed := subscribeWS(t, c, next, s, 5, queryNewBlock)
	requireTracked(t, c, 2)
	requireForwards(t, s, resubscribed)
}

// The backend forgets subscriptions even when unsubscribing fails upstream, so
// their forwarders must retire too.
func TestUnsubscribeWSRetiresWorkersOnBackendError(t *testing.T) {
	errUpstream := errors.New("upstream unsubscribe failed")
	for name, call := range map[string]func(*Client, *rpcmock.Client, *fakeWSSession) error{
		"unsubscribe": func(c *Client, next *rpcmock.Client, s *fakeWSSession) error {
			next.On("Unsubscribe", mock.Anything, s.remote, queryNewBlock).Return(errUpstream).Once()
			_, err := c.UnsubscribeWS(s.callCtx(2), queryNewBlock)
			return err
		},
		"unsubscribe_all": func(c *Client, next *rpcmock.Client, s *fakeWSSession) error {
			next.On("UnsubscribeAll", mock.Anything, s.remote).Return(errUpstream).Once()
			_, err := c.UnsubscribeAllWS(s.callCtx(2))
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, next, ctx := startWSClient(t)
			s := newFakeWSSession(ctx, "peer:1")
			removed := subscribeWS(t, c, next, s, 1, queryNewBlock)

			require.ErrorIs(t, call(c, next, s), errUpstream)

			requireRetired(t, removed)
			requireTracked(t, c, 0)
		})
	}
}

// Connections sharing a remote address, as on a unix socket, share one backend
// subscriber per query. However their calls interleave, the backend's live
// subscriber must keep its forwarder: an unread channel stalls the backend's
// single event loop.
func TestSubscribeWSSharedRemoteKeepsLiveForwarder(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(*testing.T, *Client, *rpcmock.Client, *fakeWSSession, func(mock.Arguments))
		call  func(*Client, *fakeWSSession) error
	}{
		"subscribe": {
			setup: func(_ *testing.T, _ *Client, next *rpcmock.Client, s *fakeWSSession, pause func(mock.Arguments)) {
				next.On("Subscribe", mock.Anything, s.remote, queryNewBlock).Run(pause).
					Return((<-chan coretypes.ResultEvent)(make(chan coretypes.ResultEvent)), nil).Once()
			},
			call: func(c *Client, s *fakeWSSession) error {
				_, err := c.SubscribeWS(s.callCtx(1), queryNewBlock)
				return err
			},
		},
		"unsubscribe": {
			setup: func(t *testing.T, c *Client, next *rpcmock.Client, s *fakeWSSession, pause func(mock.Arguments)) {
				subscribeWS(t, c, next, s, 1, queryNewBlock)
				next.On("Unsubscribe", mock.Anything, s.remote, queryNewBlock).Run(pause).Return(nil).Once()
			},
			call: func(c *Client, s *fakeWSSession) error {
				_, err := c.UnsubscribeWS(s.callCtx(2), queryNewBlock)
				return err
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, next, ctx := startWSClient(t)
			a, b := newFakeWSSession(ctx, "unix"), newFakeWSSession(ctx, "unix")
			paused, release := make(chan struct{}), make(chan struct{})
			tc.setup(t, c, next, a, func(mock.Arguments) { close(paused); <-release })
			done := make(chan error, 1)
			go func() { done <- tc.call(c, a) }()
			select {
			case <-paused:
			case <-time.After(5 * time.Second):
				t.Fatal("first connection's call never reached the backend")
			}

			live := subscribeWS(t, c, next, b, 3, queryNewBlock)
			close(release)
			require.NoError(t, <-done)

			requireForwards(t, b, live)
		})
	}
}
