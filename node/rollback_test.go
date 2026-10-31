package node

import (
	"context"
	"errors"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	abcimocks "github.com/dashpay/tenderdash/abci/client/mocks"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/proxy"
	"github.com/dashpay/tenderdash/internal/pubsub"
	dbm "github.com/dashpay/tenderdash/libs/db"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/service"
	"github.com/dashpay/tenderdash/types"
)

// dbCloseCounter counts Close calls for every database a node opens.
type dbCloseCounter struct {
	mtx    sync.Mutex
	closes map[string]int
}

type countedDB struct {
	dbm.DB
	id      string
	counter *dbCloseCounter
}

func (d countedDB) Close() error {
	d.counter.mtx.Lock()
	d.counter.closes[d.id]++
	d.counter.mtx.Unlock()
	return d.DB.Close()
}

func (c *dbCloseCounter) provider(ctx *config.DBContext) (dbm.DB, error) {
	db, err := config.DefaultDBProvider(ctx)
	if err != nil {
		return nil, err
	}
	c.mtx.Lock()
	defer c.mtx.Unlock()
	c.closes[ctx.ID] = 0
	return countedDB{DB: db, id: ctx.ID, counter: c}, nil
}

func (c *dbCloseCounter) closed(id string) int {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	return c.closes[id]
}

func (c *dbCloseCounter) snapshot() map[string]int {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	return maps.Clone(c.closes)
}

func (c *dbCloseCounter) requireEachClosedOnce(t *testing.T, ids ...string) {
	t.Helper()
	c.mtx.Lock()
	defer c.mtx.Unlock()
	expected := make(map[string]int, len(ids))
	for _, id := range ids {
		expected[id] = 1
	}
	require.Equal(t, expected, c.closes)
}

func rollbackTestNode(
	ctx context.Context,
	t *testing.T,
	wrapApp func(abciclient.Client) abciclient.Client,
) (*nodeImpl, *dbCloseCounter) {
	t.Helper()
	cfg, err := config.ResetTestRoot(t.TempDir(), strings.ReplaceAll(t.Name(), "/", "-"))
	require.NoError(t, err)
	cfg.RPC.ListenAddress = ""
	cfg.P2P.ListenAddress = "tcp://127.0.0.1:0"
	cfg.TxIndex.Indexer = []string{"kv"}
	key, err := types.LoadOrGenNodeKey(cfg.NodeKeyFile())
	require.NoError(t, err)
	client, _, err := proxy.ClientFactory(log.NewNopLogger(), *cfg.Abci, cfg.DBDir())
	require.NoError(t, err)
	if wrapApp != nil {
		client = wrapApp(client)
	}
	counter := &dbCloseCounter{closes: map[string]int{}}
	svc, err := makeNode(ctx, cfg, key, client, defaultGenesisDocProviderFunc(cfg), counter.provider, log.NewNopLogger())
	require.NoError(t, err)
	return svc.(*nodeImpl), counter
}

type failingStartService struct{ err error }

func (s failingStartService) Start(context.Context) error { return s.err }
func (failingStartService) IsRunning() bool               { return false }
func (failingStartService) Wait()                         {}

type failingInfoClient struct {
	abciclient.Client
	err error
}

func (c failingInfoClient) Info(context.Context, *abci.RequestInfo) (*abci.ResponseInfo, error) {
	return nil, c.err
}

var nodeDatabases = []string{"blockstore", "state", "peerstore", "evidence", "tx_index"}

// A failed start past the RPC reservation is final: it closes every database
// once, whichever children had started, and later lifecycle calls change nothing.
func TestNodeFailedStartReleasesDatabasesOnce(t *testing.T) {
	failure := errors.New("startup failed")
	testCases := map[string]func(*testing.T, *nodeImpl){
		"application start": func(t *testing.T, n *nodeImpl) {
			n.rpcEnv.ProxyApp = failedStartClient{Client: abcimocks.NewClient(t), err: failure}
		},
		"reactor start": func(_ *testing.T, n *nodeImpl) {
			n.services = append(n.services, failingStartService{err: failure})
		},
	}
	for name, inject := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			n, counter := rollbackTestNode(ctx, t, nil)
			inject(t, n)

			require.ErrorIs(t, n.Start(ctx), failure)
			counter.requireEachClosedOnce(t, nodeDatabases...)

			n.Stop()
			n.Wait()
			n.OnDrain()
			require.ErrorContains(t, n.Start(ctx), "construct a new node")
			counter.requireEachClosedOnce(t, nodeDatabases...)
		})
	}

	t.Run("handshake", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		n, counter := rollbackTestNode(ctx, t, func(app abciclient.Client) abciclient.Client {
			return failingInfoClient{Client: app, err: failure}
		})

		require.ErrorContains(t, n.Start(ctx), failure.Error())
		counter.requireEachClosedOnce(t, nodeDatabases...)
		n.Wait()
		counter.requireEachClosedOnce(t, nodeDatabases...)
	})
}

type failedStartClient struct {
	abciclient.Client
	err error
}

func (c failedStartClient) Start(context.Context) error { return c.err }
func (failedStartClient) Wait()                         {}

type waitProbeClient struct {
	abciclient.Client
	onWait func()
}

func (c waitProbeClient) Wait() { c.onWait(); c.Client.Wait() }

// The application client is the last child joined, so no node-owned database
// may be closed yet. Only the indexer, already joined, has closed its sink.
func TestNodeFailedStartReleasesDatabasesAfterJoiningChildren(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n, counter := rollbackTestNode(ctx, t, nil)
	failure := errors.New("startup failed")
	n.services = append(n.services, failingStartService{err: failure})
	var atLastJoin map[string]int
	n.rpcEnv.ProxyApp = waitProbeClient{
		Client: n.rpcEnv.ProxyApp,
		onWait: func() { atLastJoin = counter.snapshot() },
	}

	require.ErrorIs(t, n.Start(ctx), failure)
	require.Equal(t, map[string]int{"blockstore": 0, "state": 0, "peerstore": 0, "evidence": 0, "tx_index": 1}, atLastJoin)
	counter.requireEachClosedOnce(t, nodeDatabases...)
}

// An indexer whose own start fails never drains, so the node closes its sinks.
func TestNodeIndexerStartFailureClosesEventSinks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n, counter := rollbackTestNode(ctx, t, nil)
	require.NoError(t, n.rpcEnv.EventBus.Observe(ctx, func(pubsub.Message) error { return nil }))

	require.ErrorContains(t, n.Start(ctx), "an observer is already registered")
	counter.requireEachClosedOnce(t, nodeDatabases...)
}

// Normal shutdown has the same exactly-once guarantee.
func TestNodeShutdownClosesEachDatabaseOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n, counter := rollbackTestNode(ctx, t, nil)
	require.NoError(t, n.Start(ctx))
	cancel()
	n.Wait()
	counter.requireEachClosedOnce(t, nodeDatabases...)
}

func TestSeedFailedStartReleasesDatabaseOnce(t *testing.T) {
	cfg, err := config.ResetTestRoot(t.TempDir(), t.Name())
	require.NoError(t, err)
	cfg.Mode = config.ModeSeed
	cfg.P2P.ListenAddress = "tcp://127.0.0.1:0"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	key, err := types.LoadOrGenNodeKey(cfg.NodeKeyFile())
	require.NoError(t, err)
	counter := &dbCloseCounter{closes: map[string]int{}}
	svc, err := makeSeedNode(ctx, log.NewNopLogger(), cfg, counter.provider, key, defaultGenesisDocProviderFunc(cfg))
	require.NoError(t, err)
	n := svc.(*seedNodeImpl)
	failure := errors.New("PEX startup failed")
	pex := &failedSeedPEX{err: failure}
	pex.BaseService = *service.NewBaseService(log.NewNopLogger(), "test-pex", pex)
	n.pexReactor = pex

	require.ErrorIs(t, n.Start(ctx), failure)
	counter.requireEachClosedOnce(t, "peerstore")

	n.Stop()
	n.Wait()
	n.OnDrain()
	require.ErrorContains(t, n.Start(ctx), "construct a new node")
	counter.requireEachClosedOnce(t, "peerstore")
}

type waitProbeService struct {
	service.Service
	onWait func()
}

func (s waitProbeService) Wait() { s.Service.Wait(); s.onWait() }

// Seed rollback joins PEX, then the router, and only then closes the peer store.
func TestSeedFailedStartReleasesDatabaseAfterJoiningChildren(t *testing.T) {
	cfg, err := config.ResetTestRoot(t.TempDir(), t.Name())
	require.NoError(t, err)
	cfg.Mode = config.ModeSeed
	cfg.P2P.ListenAddress = "tcp://127.0.0.1:0"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	key, err := types.LoadOrGenNodeKey(cfg.NodeKeyFile())
	require.NoError(t, err)
	counter := &dbCloseCounter{closes: map[string]int{}}
	svc, err := makeSeedNode(ctx, log.NewNopLogger(), cfg, counter.provider, key, defaultGenesisDocProviderFunc(cfg))
	require.NoError(t, err)
	n := svc.(*seedNodeImpl)
	hooks := &seedRouterDrain{router: n.router, stopping: make(chan struct{}), release: make(chan struct{})}
	n.router.BaseService = service.NewBaseService(log.NewNopLogger(), "test-router", hooks)
	failure := errors.New("PEX startup failed")
	pex := &failedSeedPEX{err: failure}
	pex.BaseService = *service.NewBaseService(log.NewNopLogger(), "test-pex", pex)
	closesAtPEXJoin := make(chan int, 1)
	n.pexReactor = waitProbeService{Service: pex, onWait: func() { closesAtPEXJoin <- counter.closed("peerstore") }}

	returned := make(chan error, 1)
	go func() { returned <- n.Start(ctx) }()
	releaseRouter := sync.OnceFunc(func() { close(hooks.release) })
	defer func() { releaseRouter(); n.router.Wait(); n.Wait() }()

	select {
	case closes := <-closesAtPEXJoin:
		require.Zero(t, closes, "peer store closed before PEX was joined")
	case <-time.After(5 * time.Second):
		t.Fatal("rollback did not join PEX")
	}
	select {
	case <-hooks.stopping:
	case <-time.After(5 * time.Second):
		t.Fatal("router was not canceled")
	}
	// The router worker cannot exit until released, so rollback must still be
	// blocked joining it. The window only catches a close that arrives early.
	select {
	case err := <-returned:
		t.Fatalf("Start returned before router cleanup: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	require.Zero(t, counter.closed("peerstore"), "peer store closed before the router was joined")

	releaseRouter()
	select {
	case err := <-returned:
		require.ErrorIs(t, err, failure)
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not finish after router drained")
	}
	require.Equal(t, 1, counter.closed("peerstore"))
}
