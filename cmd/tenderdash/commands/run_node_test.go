package commands

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	cfg "github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/service"
)

type commandLifecycle struct {
	start func(context.Context) error
	stop  func()
	drain func()
}

func (s commandLifecycle) OnStart(ctx context.Context) error { return s.start(ctx) }
func (s commandLifecycle) OnStop() {
	if s.stop != nil {
		s.stop()
	}
}
func (s commandLifecycle) OnDrain() {
	if s.drain != nil {
		s.drain()
	}
}

func TestRunNodeWaitsForShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	draining := make(chan struct{})
	release := make(chan struct{})
	n := service.NewBaseService(log.NewNopLogger(), "command-test", commandLifecycle{
		start: func(context.Context) error { close(started); return nil },
		drain: func() { close(draining); <-release },
	})
	defer n.Wait()
	defer close(release)
	cmd := NewRunNodeCmd(func(context.Context, *cfg.Config, log.Logger) (service.Service, error) {
		return n, nil
	}, cfg.DefaultConfig(), log.NewNopLogger())
	cmd.SetContext(ctx)
	result := make(chan error, 1)
	go func() { result <- cmd.RunE(cmd, nil) }()
	<-started
	cancel()
	select {
	case <-draining:
	case <-time.After(time.Second):
		t.Fatal("node did not begin draining")
	}
	select {
	case err := <-result:
		t.Fatalf("command returned before final cleanup: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	// Release final cleanup without closing the channel twice in deferred cleanup.
	release <- struct{}{}
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("command did not return after node drain")
	}
}

func TestRunNodeShutdownTimeout(t *testing.T) {
	for _, blocked := range []string{"worker", "stop-hook"} {
		t.Run(blocked, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := make(chan struct{})
			release := make(chan struct{})
			var n *service.BaseService
			hooks := commandLifecycle{start: func(ctx context.Context) error {
				if blocked == "worker" {
					n.Go(ctx, func(context.Context) { <-release })
				}
				close(started)
				return nil
			}}
			if blocked == "stop-hook" {
				hooks.stop = func() { <-release }
			}
			n = service.NewBaseService(log.NewNopLogger(), "command-test", hooks)
			defer n.Wait()
			defer close(release)
			cmd := NewRunNodeCmd(func(context.Context, *cfg.Config, log.Logger) (service.Service, error) {
				return n, nil
			}, cfg.DefaultConfig(), log.NewNopLogger())
			cmd.SetContext(ctx)
			require.NoError(t, cmd.Flags().Set("shutdown-timeout", "50ms"))
			result := make(chan error, 1)
			go func() { result <- cmd.RunE(cmd, nil) }()
			<-started
			cancel()
			select {
			case err := <-result:
				require.ErrorContains(t, err, "node shutdown timed out after 50ms")
			case <-time.After(2 * time.Second):
				t.Fatal("shutdown deadline blocked behind node cleanup")
			}
		})
	}
}

func TestRunNodeStartupErrors(t *testing.T) {
	for _, failure := range []string{"provider", "start", "canceled-start"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			startupErr := errors.New("startup failed")
			n := service.NewBaseService(log.NewNopLogger(), "command-test", commandLifecycle{
				start: func(ctx context.Context) error {
					if failure == "canceled-start" {
						cancel()
						return ctx.Err()
					}
					return startupErr
				},
			})
			cmd := NewRunNodeCmd(func(context.Context, *cfg.Config, log.Logger) (service.Service, error) {
				if failure == "provider" {
					return nil, startupErr
				}
				return n, nil
			}, cfg.DefaultConfig(), log.NewNopLogger())
			cmd.SetContext(ctx)
			err := cmd.RunE(cmd, nil)
			if failure == "canceled-start" {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, startupErr)
			}
		})
	}
}

func TestRunNodeRejectsInvalidShutdownTimeout(t *testing.T) {
	for _, timeout := range []string{"0s", "-1s"} {
		t.Run(timeout, func(t *testing.T) {
			cmd := NewRunNodeCmd(func(context.Context, *cfg.Config, log.Logger) (service.Service, error) {
				t.Fatal("invalid deadline must be rejected before creating a node")
				return nil, nil
			}, cfg.DefaultConfig(), log.NewNopLogger())
			require.NoError(t, cmd.Flags().Set("shutdown-timeout", timeout))
			require.ErrorContains(t, cmd.RunE(cmd, nil), "shutdown timeout must be positive")
		})
	}
}
