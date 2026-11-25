package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
	rpcmock "github.com/dashpay/tenderdash/rpc/client/mocks"
	"github.com/dashpay/tenderdash/rpc/coretypes"
	rpctypes "github.com/dashpay/tenderdash/rpc/jsonrpc/types"
)

type callerOwnedClient struct{ *rpcmock.Client }

func (*callerOwnedClient) IsRunning() bool { return true }

type rejectingSession struct{ rpctypes.WSRPCConnection }

func (rejectingSession) Context() context.Context      { return context.Background() }
func (rejectingSession) GetRemoteAddr() string         { return "subscriber" }
func (rejectingSession) Go(func(context.Context)) bool { return false }

func TestSubscribeWSUnsubscribesRejectedAdmission(t *testing.T) {
	for _, admission := range []string{"client", "session"} {
		t.Run(admission, func(t *testing.T) {
			next := &callerOwnedClient{Client: rpcmock.NewClient(t)}
			client := NewClient(log.NewNopLogger(), next, nil)
			require.NoError(t, client.Start(context.Background()))
			t.Cleanup(func() { client.Stop(); client.Wait() })
			const query = "tm.event = 'NewBlock'"
			next.On("Subscribe", mock.Anything, "subscriber", query).
				Run(func(mock.Arguments) {
					if admission == "client" {
						client.Stop()
					}
				}).Return((<-chan coretypes.ResultEvent)(make(chan coretypes.ResultEvent)), nil).Once()
			next.On("Unsubscribe", mock.Anything, "subscriber", query).
				Run(func(args mock.Arguments) {
					ctx := args.Get(0).(context.Context)
					require.NoError(t, ctx.Err(), "cleanup needs a live context after shutdown")
					deadline, ok := ctx.Deadline()
					require.True(t, ok, "cleanup must have a deadline")
					require.LessOrEqual(t, time.Until(deadline), time.Second)
				}).Return(nil).Once()
			ctx := rpctypes.WithCallInfo(context.Background(), &rpctypes.CallInfo{WSConn: rejectingSession{}})

			_, err := client.SubscribeWS(ctx, query)
			if admission == "client" {
				require.EqualError(t, err, "light RPC client is stopping")
			} else {
				require.EqualError(t, err, "websocket session is stopping")
			}
		})
	}
}
