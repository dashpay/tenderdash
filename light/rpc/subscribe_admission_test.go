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

type stoppingSession struct {
	rejectingSession
	stop func()
}

func (s *stoppingSession) Go(func(context.Context)) bool {
	s.stop()
	return false
}

func TestSubscribeWSWaitJoinsTransaction(t *testing.T) {
	for _, stage := range []string{"subscribe", "client_rollback", "session_rollback"} {
		t.Run(stage, func(t *testing.T) {
			next := &callerOwnedClient{Client: rpcmock.NewClient(t)}
			client := NewClient(log.NewNopLogger(), next, nil)
			require.NoError(t, client.Start(context.Background()))
			t.Cleanup(func() { client.Stop(); client.Wait() })

			returned := make(chan error, 1)
			t.Cleanup(func() {
				select {
				case err := <-returned:
					require.Error(t, err)
				case <-time.After(5 * time.Second):
					t.Error("SubscribeWS did not return after upstream was released")
				}
			})

			entered, release := make(chan struct{}), make(chan struct{})
			// Release the upstream before joining workers, including on test failure.
			t.Cleanup(func() { close(release) })
			pause := func() { close(entered); <-release }
			next.On("Subscribe", mock.Anything, "subscriber", queryNewBlock).
				Run(func(mock.Arguments) {
					if stage != "session_rollback" {
						client.Stop()
					}
					if stage == "subscribe" {
						pause()
					}
				}).Return((<-chan coretypes.ResultEvent)(make(chan coretypes.ResultEvent)), nil).Once()
			next.On("Unsubscribe", mock.Anything, "subscriber", queryNewBlock).
				Run(func(mock.Arguments) {
					if stage != "subscribe" {
						pause()
					}
				}).Return(nil).Once()
			ctx := rpctypes.WithCallInfo(context.Background(), &rpctypes.CallInfo{
				WSConn: &stoppingSession{stop: client.Stop},
			})
			go func() { _, err := client.SubscribeWS(ctx, queryNewBlock); returned <- err }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("subscription transaction never reached the upstream barrier")
			}
			waited := make(chan struct{})
			go func() { client.Wait(); close(waited) }()
			select {
			case <-waited:
				t.Error("Wait returned while the subscription transaction was still using upstream")
			case <-time.After(100 * time.Millisecond):
			}
		})
	}
}

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
			requireTracked(t, client, 0)
		})
	}
}
