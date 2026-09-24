package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
)

type lifecycleService struct {
	*BaseService
	start func(context.Context) error
	stop  func()
}

func (s *lifecycleService) OnStart(ctx context.Context) error { return s.start(ctx) }
func (s *lifecycleService) OnStop() {
	if s.stop != nil {
		s.stop()
	}
}
func newLifecycleService(t *testing.T) *lifecycleService {
	s := &lifecycleService{}
	s.BaseService = NewBaseService(log.NewNopLogger(), t.Name(), s)
	return s
}

func TestManualStopCancelsStartContext(t *testing.T) {
	s := newLifecycleService(t)
	var workCtx context.Context
	s.start = func(ctx context.Context) error { workCtx = ctx; return nil }
	require.NoError(t, s.Start(context.Background()))
	s.Stop()
	s.Wait()
	require.ErrorIs(t, workCtx.Err(), context.Canceled)
}

func assertPending(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
		t.Error("Wait returned before worker cleanup completed")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestWaitDrainsManagedWork(t *testing.T) {
	for _, parentStop := range []bool{false, true} {
		t.Run(map[bool]string{false: "manual", true: "parent"}[parentStop], func(t *testing.T) {
			s := newLifecycleService(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			s.start = func(ctx context.Context) error {
				require.True(t, s.Go(ctx, func(ctx context.Context) { <-ctx.Done(); close(entered); <-release }))
				return nil
			}
			require.NoError(t, s.Start(ctx))
			if parentStop {
				cancel()
			} else {
				s.Stop()
			}
			<-entered
			done := make(chan struct{})
			go func() { s.Wait(); close(done) }()
			assertPending(t, done)
			close(release)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("worker was not drained")
			}
			require.False(t, s.IsRunning())
			require.ErrorIs(t, s.Start(ctx), errAlreadyStopped)
		})
	}
}

func TestFailedStartDrainsBeforeRetry(t *testing.T) {
	s := newLifecycleService(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var old context.Context
	s.start = func(ctx context.Context) error {
		old = ctx
		require.True(t, s.Go(ctx, func(ctx context.Context) { <-ctx.Done(); close(entered); <-release }))
		return context.DeadlineExceeded
	}
	s.Wait()
	done := make(chan struct{})
	go func() { defer close(done); require.ErrorIs(t, s.Start(context.Background()), context.DeadlineExceeded) }()
	<-entered
	assertPending(t, done)
	close(release)
	<-done
	s.Wait()
	s.start = func(_ context.Context) error {
		require.False(t, s.Go(old, func(context.Context) { t.Error("old attempt admitted") }))
		return nil
	}
	require.NoError(t, s.Start(context.Background()))
	require.True(t, s.IsRunning())
	s.Stop()
	s.Wait()
}

func TestWorkerCanStopAndHookCanInspectLifecycle(t *testing.T) {
	s := newLifecycleService(t)
	s.stop = func() { require.False(t, s.IsRunning()); s.Stop() }
	s.start = func(ctx context.Context) error {
		require.False(t, s.IsRunning())
		require.True(t, s.Go(ctx, func(context.Context) { s.Stop() }))
		return nil
	}
	require.NoError(t, s.Start(context.Background()))
	done := make(chan struct{})
	go func() { s.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("lifecycle deadlock")
	}
}

func TestRegistrationRacesStop(t *testing.T) {
	for range 100 {
		s := newLifecycleService(t)
		var ctx context.Context
		s.start = func(c context.Context) error { ctx = c; return nil }
		require.NoError(t, s.Start(context.Background()))
		admittedDone := make(chan struct{})
		require.True(t, s.Go(ctx, func(ctx context.Context) { <-ctx.Done(); close(admittedDone) }))
		registered := make(chan struct{})
		go func() {
			defer close(registered)
			for range 100 {
				s.Go(ctx, func(ctx context.Context) { <-ctx.Done() })
			}
		}()
		s.Stop()
		s.Wait()
		<-registered
		select {
		case <-admittedDone:
		default:
			t.Error("admitted work escaped drain")
		}
		require.False(t, s.Go(ctx, func(context.Context) { t.Error("work admitted after stop") }))
	}
}

type finalizingService struct {
	*lifecycleService
	drain func()
}

func (s *finalizingService) OnDrain() { s.drain() }

func TestFinalizerFollowsWorkerCleanup(t *testing.T) {
	workerDone := make(chan struct{})
	release := make(chan struct{})
	finalizerEntered := make(chan struct{})
	s := &finalizingService{lifecycleService: newLifecycleService(t)}
	s.BaseService = NewBaseService(log.NewNopLogger(), t.Name(), s)
	s.start = func(ctx context.Context) error {
		require.True(t, s.Go(ctx, func(ctx context.Context) { <-ctx.Done(); close(workerDone) }))
		return nil
	}
	s.drain = func() {
		select {
		case <-workerDone:
		default:
			t.Error("finalizer ran before worker cleanup")
		}
		close(finalizerEntered)
		<-release
	}
	require.NoError(t, s.Start(context.Background()))
	s.Stop()
	<-finalizerEntered
	done := make(chan struct{})
	go func() { s.Wait(); close(done) }()
	assertPending(t, done)
	close(release)
	<-done
}

func TestStopDuringStartup(t *testing.T) {
	s := newLifecycleService(t)
	entered, release := make(chan struct{}), make(chan struct{})
	stopped := make(chan struct{})
	s.start = func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		<-release
		return nil
	}
	s.stop = func() { close(stopped) }
	started := make(chan error, 1)
	go func() { started <- s.Start(context.Background()) }()
	<-entered
	require.ErrorIs(t, s.Start(context.Background()), errStarting)
	s.Stop()
	select {
	case <-stopped:
		t.Fatal("OnStop ran concurrently with OnStart")
	default:
	}
	close(release)
	require.NoError(t, <-started)
	s.Wait()
	<-stopped
	require.False(t, s.IsRunning())
}

func TestStartContextPreservesValuesAndDeadline(t *testing.T) {
	type key struct{}
	deadline := time.Now().Add(time.Hour)
	parent, cancel := context.WithDeadline(context.WithValue(context.Background(), key{}, "value"), deadline)
	defer cancel()
	s := newLifecycleService(t)
	s.start = func(ctx context.Context) error {
		require.Equal(t, "value", ctx.Value(key{}))
		got, ok := ctx.Deadline()
		require.True(t, ok)
		require.Equal(t, deadline, got)
		return nil
	}
	require.False(t, s.Go(parent, func(context.Context) { t.Error("work before Start") }))
	require.NoError(t, s.Start(parent))
	require.NoError(t, s.Start(parent))
	s.Stop()
	s.Wait()
	require.NoError(t, parent.Err())
}
