package core

import (
	"context"
	"errors"
	"sync"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/rpc/coretypes"
)

type asyncBroadcasts struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	limit  int
	active int
	idle   chan struct{}
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
	a.limit = env.Config.MaxConcurrentBroadcastTxAsync
	if a.limit == 0 {
		a.limit = config.DefaultRPCConfig().MaxConcurrentBroadcastTxAsync
	}
	a.ctx, a.cancel = context.WithCancel(ctx)
	a.idle = make(chan struct{})
	close(a.idle)
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
	idle := a.idle
	a.mu.Unlock()
	select {
	case <-idle:
		return nil
	default:
	}
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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
	if a.active >= a.limit {
		return nil, coretypes.ErrTooManyRequests
	}
	if a.active == 0 {
		a.idle = make(chan struct{})
	}
	a.active++
	return a.ctx, nil
}

func (a *asyncBroadcasts) release() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.active--
	if a.active == 0 {
		close(a.idle)
	}
}
