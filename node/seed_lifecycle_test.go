package node

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/service"
	"github.com/dashpay/tenderdash/types"
)

type seedRouterDrain struct {
	router   *p2p.Router
	stopping chan struct{}
	release  chan struct{}
}

func (r *seedRouterDrain) OnStart(ctx context.Context) error {
	if err := r.router.OnStart(ctx); err != nil {
		return err
	}
	r.router.Go(ctx, func(ctx context.Context) {
		<-ctx.Done()
		close(r.stopping)
		<-r.release
	})
	return nil
}
func (r *seedRouterDrain) OnStop() { r.router.OnStop() }

type failedSeedPEX struct {
	service.BaseService
	err error
}

func (p *failedSeedPEX) OnStart(context.Context) error { return p.err }
func (p *failedSeedPEX) OnStop()                       {}

func TestSeedFailedStartDrainsRouter(t *testing.T) {
	cfg, err := config.ResetTestRoot(t.TempDir(), t.Name())
	require.NoError(t, err)
	cfg.Mode = config.ModeSeed
	cfg.P2P.ListenAddress = "tcp://127.0.0.1:0"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	key, err := types.LoadOrGenNodeKey(cfg.NodeKeyFile())
	require.NoError(t, err)
	svc, err := makeSeedNode(ctx, log.NewNopLogger(), cfg, config.DefaultDBProvider, key, defaultGenesisDocProviderFunc(cfg))
	require.NoError(t, err)
	n := svc.(*seedNodeImpl)
	defer func() { require.NoError(t, n.shutdownOps()) }()
	hooks := &seedRouterDrain{router: n.router, stopping: make(chan struct{}), release: make(chan struct{})}
	n.router.BaseService = service.NewBaseService(log.NewNopLogger(), "test-router", hooks)
	failure := errors.New("PEX startup failed")
	pex := &failedSeedPEX{err: failure}
	pex.BaseService = *service.NewBaseService(log.NewNopLogger(), "test-pex", pex)
	n.pexReactor = pex
	returned := make(chan error, 1)
	go func() { returned <- n.Start(ctx) }()
	var release sync.Once
	defer func() { release.Do(func() { close(hooks.release) }); n.router.Wait(); n.Wait() }()
	select {
	case <-hooks.stopping:
	case <-time.After(5 * time.Second):
		t.Fatal("router was not canceled")
	}
	select {
	case err := <-returned:
		t.Fatalf("Start returned before router cleanup: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	release.Do(func() { close(hooks.release) })
	select {
	case err := <-returned:
		require.ErrorIs(t, err, failure)
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not finish after router drained")
	}
}
