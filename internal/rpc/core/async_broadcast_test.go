package core

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/rpc/coretypes"
)

func TestAsyncBroadcastRestartAfterDrain(t *testing.T) {
	env := &Environment{Config: *config.DefaultRPCConfig()}
	require.NoError(t, env.StartAsyncBroadcasts(t.Context()))
	_, err := env.asyncBroadcasts.acquire(t.Context())
	require.NoError(t, err)
	stopped, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, env.StopAsyncBroadcasts(stopped), context.Canceled)
	require.Error(t, env.StartAsyncBroadcasts(t.Context()), "outstanding work must prevent restart")
	env.asyncBroadcasts.release()
	require.NoError(t, env.StopAsyncBroadcasts(stopped))
	_, err = env.asyncBroadcasts.acquire(t.Context())
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, env.StartAsyncBroadcasts(t.Context()))
	_, err = env.asyncBroadcasts.acquire(t.Context())
	require.NoError(t, err)
	env.asyncBroadcasts.release()
	require.NoError(t, env.StopAsyncBroadcasts(t.Context()))
}

func TestAsyncBroadcastOldStopCannotResetRestart(t *testing.T) {
	cfg := *config.DefaultRPCConfig()
	cfg.MaxConcurrentBroadcastTxAsync = 1
	env := &Environment{Config: cfg}
	require.NoError(t, env.StartAsyncBroadcasts(t.Context()))
	_, err := env.asyncBroadcasts.acquire(t.Context())
	require.NoError(t, err)

	entered, resume := make(chan struct{}), make(chan struct{})
	resumeFn := sync.OnceFunc(func() { close(resume) })
	defer resumeFn()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	paused := &pausedDoneContext{Context: ctx, entered: entered, resume: resume}
	result := make(chan error, 1)
	go func() { result <- env.StopAsyncBroadcasts(paused) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("stop did not begin waiting for admitted work")
	}
	env.asyncBroadcasts.release()
	require.NoError(t, env.StopAsyncBroadcasts(ctx))
	require.NoError(t, env.StartAsyncBroadcasts(ctx))
	_, err = env.asyncBroadcasts.acquire(ctx)
	require.NoError(t, err)
	defer env.asyncBroadcasts.release()

	resumeFn()
	require.NoError(t, <-result)
	require.Error(t, env.StartAsyncBroadcasts(ctx), "old stop must not clear the new started state")
	_, err = env.asyncBroadcasts.acquire(ctx)
	require.ErrorIs(t, err, coretypes.ErrTooManyRequests)
}

// Pause a stop after it captures its admission state but before it waits for slots.
type pausedDoneContext struct {
	context.Context
	entered chan struct{}
	resume  <-chan struct{}
	once    sync.Once
}

func (c *pausedDoneContext) Done() <-chan struct{} {
	c.once.Do(func() {
		close(c.entered)
		<-c.resume
	})
	return c.Context.Done()
}
