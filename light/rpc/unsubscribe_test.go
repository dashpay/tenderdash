package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
	rpcmock "github.com/dashpay/tenderdash/rpc/client/mocks"
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
