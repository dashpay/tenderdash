package rpc_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/eventbus"
	rpccore "github.com/dashpay/tenderdash/internal/rpc/core"
	"github.com/dashpay/tenderdash/libs/log"
	lrpc "github.com/dashpay/tenderdash/light/rpc"
	rpcclient "github.com/dashpay/tenderdash/rpc/client"
	"github.com/dashpay/tenderdash/rpc/client/local"
)

type localNode struct{ bus *eventbus.EventBus }

func (n localNode) RPCEnvironment() *rpccore.Environment { return &rpccore.Environment{} }
func (n localNode) EventBus() *eventbus.EventBus         { return n.bus }

func requireClientWaitReturns(t *testing.T, client *lrpc.Client) {
	t.Helper()
	waited := make(chan struct{})
	go func() { client.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("light client Wait blocked on a service it does not own")
	}
}

// A local client is its node's event bus. Stopping only the wrapper must neither
// wait for that bus nor stop it.
func TestClientWaitLeavesCallerOwnedEventBusRunning(t *testing.T) {
	stoppers := map[string]func(*lrpc.Client, context.CancelFunc){
		"cancel": func(_ *lrpc.Client, cancel context.CancelFunc) { cancel() },
		"stop":   func(client *lrpc.Client, _ context.CancelFunc) { client.Stop() },
	}
	for name, stop := range stoppers {
		t.Run(name, func(t *testing.T) {
			logger := log.NewNopLogger()
			busCtx, stopBus := context.WithCancel(context.Background())
			bus := eventbus.NewDefault(logger)
			require.NoError(t, bus.Start(busCtx))
			defer func() { stopBus(); bus.Wait() }()
			next, err := local.New(logger, localNode{bus: bus})
			require.NoError(t, err)
			client := lrpc.NewClient(logger, next, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			require.NoError(t, client.Start(ctx))

			stop(client, cancel)
			requireClientWaitReturns(t, client)
			require.True(t, bus.IsRunning(), "stopping the wrapper stopped the shared event bus")
		})
	}
}

// lifecycleClient records the join of a wrapped client exposing its running state.
type lifecycleClient struct {
	rpcclient.Client
	running bool
	waited  atomic.Bool
}

func (c *lifecycleClient) Start(context.Context) error { return nil }
func (c *lifecycleClient) IsRunning() bool             { return c.running }
func (c *lifecycleClient) Wait()                       { c.waited.Store(true) }

// The wrapper joins exactly the client its own start attempt started.
func TestClientJoinsOnlyClientItStarted(t *testing.T) {
	for _, alreadyRunning := range []bool{false, true} {
		next := &lifecycleClient{running: alreadyRunning}
		client := lrpc.NewClient(log.NewNopLogger(), next, nil)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		require.NoError(t, client.Start(ctx))

		cancel()
		requireClientWaitReturns(t, client)
		require.Equal(t, !alreadyRunning, next.waited.Load(), "already running: %t", alreadyRunning)
	}
}
