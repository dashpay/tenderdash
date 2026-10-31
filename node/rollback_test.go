package node

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	abcimocks "github.com/dashpay/tenderdash/abci/client/mocks"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/proxy"
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
