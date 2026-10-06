package service

import (
	"context"
	"errors"
	stdsync "sync"
	"sync/atomic"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/dashpay/tenderdash/libs/log"
)

var (
	errAlreadyStopped         = errors.New("already stopped")
	errStarting               = errors.New("service is starting")
	_                 Service = (*BaseService)(nil)
)

// Service defines a service that can be started and stopped.
type Service interface {
	Start(context.Context) error
	IsRunning() bool
	Wait()
}

// Implementation supplies startup and shutdown hooks. Hooks run without the
// lifecycle lock. They must not call Wait on their own service.
type Implementation interface {
	OnStart(context.Context) error
	// OnStop unblocks workers. It must not wait for managed workers.
	OnStop()
}

// Finalizer releases resources after OnStop and all managed workers finish.
// It is called only after a successful OnStart, before Wait returns.
type Finalizer interface {
	OnDrain()
}

type attemptKey struct{}

type serviceAttempt struct {
	cancel     context.CancelFunc
	stoppingCh <-chan struct{}
	done       chan struct{}
	workers    stdsync.WaitGroup
	starting   bool
	stopping   bool
}

// BaseService owns a service context and work registered through Go. Stop cancels
// that context and calls OnStop; Wait joins work and optional OnDrain cleanup.
// Plain go statements are not tracked. A successful start cannot be restarted.
// A failed OnStart must release its own resources; BaseService cancels and joins
// its managed work before allowing another attempt.
type BaseService struct {
	logger  log.Logger
	name    string
	mtx     sync.Mutex
	attempt *serviceAttempt
	running uint32
	impl    Implementation
}

// NewBaseService creates a new BaseService.
func NewBaseService(logger log.Logger, name string, impl Implementation) *BaseService {
	return &BaseService{logger: logger, name: name, impl: impl}
}

// Start calls OnStart with a context canceled by either the parent or Stop.
// Repeated calls while running succeed; concurrent startup returns an error.
func (bs *BaseService) Start(ctx context.Context) error {
	bs.mtx.Lock()
	if a := bs.attempt; a != nil {
		var err error
		switch {
		case a.stopping:
			err = errAlreadyStopped
		case a.starting:
			err = errStarting
		}
		bs.mtx.Unlock()
		return err
	}
	workCtx, cancel := context.WithCancel(ctx)
	a := &serviceAttempt{cancel: cancel, stoppingCh: workCtx.Done(), done: make(chan struct{}), starting: true}
	bs.attempt = a
	workCtx = context.WithValue(workCtx, attemptKey{}, a)
	bs.mtx.Unlock()

	bs.logger.Info("starting service", "service", bs.name)
	err := bs.impl.OnStart(workCtx)
	bs.mtx.Lock()
	a.starting = false
	if err != nil {
		a.stopping = true
		bs.mtx.Unlock()
		cancel()
		a.workers.Wait()
		bs.mtx.Lock()
		close(a.done)
		bs.attempt = nil
		bs.mtx.Unlock()
		return err
	}
	stopping := a.stopping
	if !stopping {
		atomic.StoreUint32(&bs.running, 1)
	}
	bs.mtx.Unlock()
	if stopping {
		bs.finishStop(a)
	} else {
		go func() {
			select {
			case <-workCtx.Done():
				bs.stopAttempt(a)
			case <-a.done:
			}
		}()
	}
	return nil
}

// Go registers work belonging to the context received by OnStart (or a child).
// It returns false before startup, after stopping begins, or for an old attempt.
// A worker may call Stop, but must not call Wait on its own service.
func (bs *BaseService) Go(ctx context.Context, fn func(context.Context)) bool {
	bs.mtx.Lock()
	defer bs.mtx.Unlock()
	a := bs.attempt
	if a == nil || a.stopping || ctx.Value(attemptKey{}) != a {
		return false
	}
	a.workers.Add(1)
	go func() { defer a.workers.Done(); fn(ctx) }()
	return true
}

// Stop cancels work and invokes OnStop once, without waiting for workers.
// During startup it requests shutdown after OnStart returns. Concurrent or
// recursive calls return immediately; use Wait to observe complete shutdown.
func (bs *BaseService) Stop() {
	bs.mtx.Lock()
	a := bs.attempt
	bs.mtx.Unlock()
	bs.stopAttempt(a)
}

func (bs *BaseService) stopAttempt(a *serviceAttempt) {
	bs.mtx.Lock()
	if a == nil || bs.attempt != a || a.stopping {
		bs.mtx.Unlock()
		return
	}
	a.stopping = true
	atomic.StoreUint32(&bs.running, 0)
	starting := a.starting
	bs.mtx.Unlock()
	a.cancel()
	if !starting {
		bs.finishStop(a)
	}
}

func (bs *BaseService) finishStop(a *serviceAttempt) {
	bs.logger.Info("stopping service", "service", bs.name)
	bs.impl.OnStop()
	go func() {
		a.workers.Wait()
		if finalizer, ok := bs.impl.(Finalizer); ok {
			finalizer.OnDrain()
		}
		bs.logger.Info("stopped service", "service", bs.name)
		close(a.done)
	}()
}

// IsRunning reports successful startup until shutdown is requested. False does
// not imply cleanup has completed; use Wait before releasing owned resources.
func (bs *BaseService) IsRunning() bool { return atomic.LoadUint32(&bs.running) == 1 }

// Stopping signals context cancellation, not completed shutdown. It is nil
// before Start and after a failed attempt has drained; use Wait for completion.
func (bs *BaseService) Stopping() <-chan struct{} {
	bs.mtx.Lock()
	defer bs.mtx.Unlock()
	if bs.attempt == nil {
		return nil
	}
	return bs.attempt.stoppingCh
}

// Wait joins the current startup attempt, shutdown hooks and registered work.
// It returns immediately before Start or after a failed Start has drained.
func (bs *BaseService) Wait() {
	bs.mtx.Lock()
	a := bs.attempt
	bs.mtx.Unlock()
	if a != nil {
		<-a.done
	}
}

// String provides a human-friendly representation of the service.
func (bs *BaseService) String() string { return bs.name }
