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

// blockUntilCanceled stands in for an upstream WebSocket send with no writer.
func blockUntilCanceled(args mock.Arguments) {
	<-args.Get(0).(context.Context).Done()
}

// Unsubscribe calls run inside a joined WebSocket session, so they must stop
// when the caller's request context is canceled.
func TestUnsubscribeWSObservesCallerCancellation(t *testing.T) {
	for name, call := range map[string]func(*Client, context.Context) error{
		"unsubscribe": func(c *Client, ctx context.Context) error {
			_, err := c.UnsubscribeWS(ctx, "tm.event = 'NewBlock'")
			return err
		},
		"unsubscribe_all": func(c *Client, ctx context.Context) error {
			_, err := c.UnsubscribeAllWS(ctx)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			next := rpcmock.NewClient(t)
			next.On("Unsubscribe", mock.Anything, mock.Anything, mock.Anything).
				Run(blockUntilCanceled).Return(context.Canceled).Maybe()
			next.On("UnsubscribeAll", mock.Anything, mock.Anything).
				Run(blockUntilCanceled).Return(context.Canceled).Maybe()
			c := NewClient(log.NewNopLogger(), next, nil)
			ctx, cancel := context.WithCancel(rpctypes.WithCallInfo(context.Background(), &rpctypes.CallInfo{}))
			done := make(chan error, 1)
			go func() { done <- call(c, ctx) }()
			cancel()
			select {
			case err := <-done:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(5 * time.Second):
				t.Fatal("unsubscribe ignored caller cancellation")
			}
		})
	}
}

func TestUnsubscribeWSWithoutConnection(t *testing.T) {
	for _, method := range []string{"Unsubscribe", "UnsubscribeAll"} {
		for name, ctx := range map[string]context.Context{
			"plain_context":  context.Background(),
			"nil_connection": rpctypes.WithCallInfo(context.Background(), &rpctypes.CallInfo{}),
		} {
			for outcome, backendErr := range map[string]error{
				"success": nil,
				"error":   errors.New("upstream unsubscribe failed"),
			} {
				t.Run(method+"/"+name+"/"+outcome, func(t *testing.T) {
					c, next, sessionCtx := startWSClient(t)
					s := newFakeWSSession(sessionCtx, "")
					live := subscribeWS(t, c, next, s, 1, queryNewBlock)
					args := []interface{}{mock.Anything, ""}
					if method == "Unsubscribe" {
						args = append(args, queryNewBlock)
					}
					next.On(method, args...).Return(backendErr).Once()

					var result *coretypes.ResultUnsubscribe
					var err error
					require.NotPanics(t, func() {
						if method == "Unsubscribe" {
							result, err = c.UnsubscribeWS(ctx, queryNewBlock)
						} else {
							result, err = c.UnsubscribeAllWS(ctx)
						}
					})
					require.Equal(t, backendErr, err)
					if backendErr == nil {
						require.Equal(t, &coretypes.ResultUnsubscribe{}, result)
					} else {
						require.Nil(t, result)
					}
					requireForwards(t, s, live)
				})
			}
		}
	}
}

func TestUnsubscribeWSTimeoutRetiresWorkers(t *testing.T) {
	for _, method := range []string{"Unsubscribe", "UnsubscribeAll"} {
		t.Run(method, func(t *testing.T) {
			c, next, ctx := startWSClient(t)
			s := newFakeWSSession(ctx, "peer:1")
			removed := subscribeWS(t, c, next, s, 1, queryNewBlock)
			var backendCtx context.Context
			block := func(args mock.Arguments) {
				backendCtx = args.Get(0).(context.Context)
				blockUntilCanceled(args)
			}
			if method == "Unsubscribe" {
				next.On(method, mock.Anything, s.remote, queryNewBlock).Run(block).
					Return(func(ctx context.Context, _, _ string) error { return ctx.Err() }).Once()
			} else {
				next.On(method, mock.Anything, s.remote).Run(block).
					Return(func(ctx context.Context, _ string) error { return ctx.Err() }).Once()
			}
			requestCtx, cancel := context.WithCancel(s.callCtx(2))
			defer cancel()
			// Release a broken handler without ending the session or its workers.
			watchdog := time.AfterFunc(3*time.Second, cancel)
			defer watchdog.Stop()
			started := time.Now()
			var result *coretypes.ResultUnsubscribe
			var err error
			if method == "Unsubscribe" {
				result, err = c.UnsubscribeWS(requestCtx, queryNewBlock)
			} else {
				result, err = c.UnsubscribeAllWS(requestCtx)
			}
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.Nil(t, result)
			require.Less(t, time.Since(started), 2*time.Second)
			deadline, ok := backendCtx.Deadline()
			require.True(t, ok)
			require.WithinDuration(t, started.Add(time.Second), deadline, 100*time.Millisecond)
			require.NoError(t, requestCtx.Err())
			requireRetired(t, removed)
			requireTracked(t, c, 0)
		})
	}
}
