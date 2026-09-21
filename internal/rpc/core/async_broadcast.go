package core

import (
	"context"
	"errors"
	"sync"

	"golang.org/x/sync/semaphore"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/rpc/coretypes"
)

type asyncBroadcasts struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	limit  int64
	slots  *semaphore.Weighted
}

// StartAsyncBroadcasts initializes async admission before exposing the environment to clients.
func (env *Environment) StartAsyncBroadcasts(ctx context.Context) error {
	a := &env.asyncBroadcasts
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ctx != nil {
		return errors.New("async broadcasts already started")
	}
	if err := env.Config.ValidateBasic(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	a.limit = int64(env.Config.MaxConcurrentBroadcastTxAsync)
	if a.limit == 0 {
		a.limit = int64(config.DefaultRPCConfig().MaxConcurrentBroadcastTxAsync)
	}
	a.ctx, a.cancel = context.WithCancel(ctx)
	a.slots = semaphore.NewWeighted(a.limit)
	return nil
}

// StopAsyncBroadcasts closes admission and waits for canceled jobs until ctx expires.
func (env *Environment) StopAsyncBroadcasts(ctx context.Context) error {
	a := &env.asyncBroadcasts
	a.mu.Lock()
	if a.cancel == nil {
		a.mu.Unlock()
		return nil
	}
	a.cancel()
	a.mu.Unlock()
	// Taking every slot waits for all admitted calls; the fast path also works with an expired ctx.
	if !a.slots.TryAcquire(a.limit) {
		if err := a.slots.Acquire(ctx, a.limit); err != nil {
			return err
		}
	}
	a.slots.Release(a.limit)
	return nil
}

func (a *asyncBroadcasts) acquire(ctx context.Context) (context.Context, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a.ctx == nil {
		return nil, errors.New("async broadcasts not started")
	}
	if err := a.ctx.Err(); err != nil {
		return nil, err
	}
	if !a.slots.TryAcquire(1) {
		return nil, coretypes.ErrTooManyRequests
	}
	return a.ctx, nil
}

func (a *asyncBroadcasts) release() {
	a.slots.Release(1)
}
