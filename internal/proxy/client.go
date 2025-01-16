package proxy

import (
	"context"
	"io"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/go-kit/kit/metrics"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	"github.com/dashpay/tenderdash/abci/example/kvstore"
	"github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/libs/log"
	tmos "github.com/dashpay/tenderdash/libs/os"
	"github.com/dashpay/tenderdash/libs/service"
	e2e "github.com/dashpay/tenderdash/test/e2e/app"
)

// ClientFactory returns a client object, which will create a local
// client if addr is one of: 'kvstore', 'persistent_kvstore', 'e2e',
// or 'noop', otherwise - a remote client.
//
// The Closer is a noop except for persistent_kvstore applications,
// which will clean up the store.
func ClientFactory(logger log.Logger, cfg config.AbciConfig, dbDir string) (abciclient.Client, io.Closer, error) {
	addr := cfg.Address

	switch addr {
	case "kvstore":
		app, err := kvstore.NewMemoryApp(
			kvstore.WithLogger(logger.With("module", "kvstore")),
		)
		if err != nil {
			return nil, nil, err
		}
		return abciclient.NewLocalClient(logger, app), tmos.NoopCloser{}, nil
	case "persistent_kvstore":
		app, err := kvstore.NewPersistentApp(
			kvstore.DefaultConfig(dbDir),
			kvstore.WithLogger(logger.With("module", "kvstore")),
		)
		if err != nil {
			return nil, nil, err
		}
		return abciclient.NewLocalClient(logger, app), app, nil
	case "e2e":
		app, err := e2e.NewApplication(kvstore.DefaultConfig(dbDir))
		if err != nil {
			return nil, tmos.NoopCloser{}, err
		}
		return abciclient.NewLocalClient(logger, app), tmos.NoopCloser{}, nil
	case "noop":
		return abciclient.NewLocalClient(logger, types.NewBaseApplication()), tmos.NoopCloser{}, nil
	default:
		const mustConnect = false // loop retrying
		client, err := abciclient.NewClient(logger, cfg, mustConnect)
		if err != nil {
			return nil, tmos.NoopCloser{}, err
		}

		return client, tmos.NoopCloser{}, nil
	}
}

// proxyClient provides the application connection.
type proxyClient struct {
	service.BaseService
	logger log.Logger

	client  abciclient.Client
	metrics *Metrics
}

// New creates a proxy application interface.
func New(client abciclient.Client, logger log.Logger, metrics *Metrics) abciclient.Client {
	conn := &proxyClient{
		logger:  logger,
		metrics: metrics,
		client:  client,
	}
	conn.BaseService = *service.NewBaseService(logger, "proxyClient", conn)
	return conn
}

func (app *proxyClient) OnStop()      { tryCallStop(app.client) }
func (app *proxyClient) Error() error { return app.client.Error() }

func tryCallStop(client abciclient.Client) {
	if c, ok := client.(interface{ Stop() }); ok {
		c.Stop()
	}
}

func (app *proxyClient) OnStart(ctx context.Context) error {
	var err error
	defer func() {
		if err != nil {
			tryCallStop(app.client)
		}
	}()

	// Kill Tendermint if the ABCI application crashes.
	go func() {
		if !app.client.IsRunning() {
			return
		}
		app.client.Wait()
		if ctx.Err() != nil {
			return
		}

		if err := app.client.Error(); err != nil {
			app.logger.Error("client connection terminated. Did the application crash? Please restart tendermint",
				"err", err)

			if killErr := kill(); killErr != nil {
				app.logger.Error("Failed to kill this process - please do so manually",
					"err", killErr)
			}
		}

	}()

	return app.client.Start(ctx)
}

func kill() error {
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		return err
	}

	return p.Signal(syscall.SIGABRT)
}

func (app *proxyClient) InitChain(ctx context.Context, req *types.RequestInitChain) (r *types.ResponseInitChain, err error) {
	defer addTimeSample(app.metrics.MethodTiming.With("method", "init_chain", "type", "sync", "success"))(&err)
	return app.client.InitChain(ctx, req)
}

func (app *proxyClient) PrepareProposal(ctx context.Context, req *types.RequestPrepareProposal) (r *types.ResponsePrepareProposal, err error) {
	defer addTimeSample(app.metrics.MethodTiming.With("method", "prepare_proposal", "type", "sync"))(&err)
	return app.client.PrepareProposal(ctx, req)
}

func (app *proxyClient) ProcessProposal(ctx context.Context, req *types.RequestProcessProposal) (r *types.ResponseProcessProposal, err error) {
	defer addTimeSample(app.metrics.MethodTiming.With("method", "process_proposal", "type", "sync"))(&err)
	return app.client.ProcessProposal(ctx, req)
}

func (app *proxyClient) ExtendVote(ctx context.Context, req *types.RequestExtendVote) (r *types.ResponseExtendVote, err error) {
	defer addTimeSample(app.metrics.MethodTiming.With("method", "extend_vote", "type", "sync"))(&err)
	return app.client.ExtendVote(ctx, req)
}

func (app *proxyClient) VerifyVoteExtension(ctx context.Context, req *types.RequestVerifyVoteExtension) (r *types.ResponseVerifyVoteExtension, err error) {
	defer addTimeSample(app.metrics.MethodTiming.With("method", "verify_vote_extension", "type", "sync"))(&err)
	return app.client.VerifyVoteExtension(ctx, req)
}

func (app *proxyClient) FinalizeBlock(ctx context.Context, req *types.RequestFinalizeBlock) (r *types.ResponseFinalizeBlock, err error) {
	defer addTimeSample(app.metrics.MethodTiming.With("method", "finalize_block", "type", "sync"))(&err)
	return app.client.FinalizeBlock(ctx, req)
}

func (app *proxyClient) Flush(ctx context.Context) (err error) {
	defer addTimeSample(app.metrics.MethodTiming.With("method", "flush", "type", "sync"))(&err)
	return app.client.Flush(ctx)
}

func (app *proxyClient) CheckTx(ctx context.Context, req *types.RequestCheckTx) (r *types.ResponseCheckTx, err error) {
	defer addTimeSample(app.metrics.MethodTiming.With("method", "check_tx", "type", "sync"))(&err)
	return app.client.CheckTx(ctx, req)
}

func (app *proxyClient) Echo(ctx context.Context, msg string) (r *types.ResponseEcho, err error) {
	defer addTimeSample(app.metrics.MethodTiming.With("method", "echo", "type", "sync"))(&err)
	return app.client.Echo(ctx, msg)
}

func (app *proxyClient) Info(ctx context.Context, req *types.RequestInfo) (r *types.ResponseInfo, err error) {
	defer addTimeSample(app.metrics.MethodTiming.With("method", "info", "type", "sync"))(&err)
	return app.client.Info(ctx, req)
}

func (app *proxyClient) Query(ctx context.Context, req *types.RequestQuery) (r *types.ResponseQuery, err error) {
	defer addTimeSample(app.metrics.MethodTiming.With("method", "query", "type", "sync"))(&err)
	return app.client.Query(ctx, req)
}

func (app *proxyClient) ListSnapshots(ctx context.Context, req *types.RequestListSnapshots) (r *types.ResponseListSnapshots, err error) {
	defer addTimeSample(app.metrics.MethodTiming.With("method", "list_snapshots", "type", "sync"))(&err)
	return app.client.ListSnapshots(ctx, req)
}

func (app *proxyClient) OfferSnapshot(ctx context.Context, req *types.RequestOfferSnapshot) (r *types.ResponseOfferSnapshot, err error) {
	defer addTimeSample(app.metrics.MethodTiming.With("method", "offer_snapshot", "type", "sync"))(&err)
	return app.client.OfferSnapshot(ctx, req)
}

func (app *proxyClient) LoadSnapshotChunk(ctx context.Context, req *types.RequestLoadSnapshotChunk) (r *types.ResponseLoadSnapshotChunk, err error) {
	defer addTimeSample(app.metrics.MethodTiming.With("method", "load_snapshot_chunk", "type", "sync"))(&err)
	return app.client.LoadSnapshotChunk(ctx, req)
}

func (app *proxyClient) ApplySnapshotChunk(ctx context.Context, req *types.RequestApplySnapshotChunk) (r *types.ResponseApplySnapshotChunk, err error) {
	defer addTimeSample(app.metrics.MethodTiming.With("method", "apply_snapshot_chunk", "type", "sync"))(&err)
	return app.client.ApplySnapshotChunk(ctx, req)
}

func (app *proxyClient) FinalizeSnapshot(ctx context.Context, req *types.RequestFinalizeSnapshot) (r *types.ResponseFinalizeSnapshot, err error) {
	defer addTimeSample(app.metrics.MethodTiming.With("method", "finalize_snapshot", "type", "sync"))(&err)
	return app.client.FinalizeSnapshot(ctx, req)
}

// addTimeSample returns a function that, when called, adds an observation to m.
// The observation added to m is the number of seconds ellapsed since addTimeSample
// was initially called. addTimeSample is meant to be called in a defer to calculate
// the amount of time a function takes to complete.
func addTimeSample(m metrics.Histogram) func(*error) {
	start := time.Now()

	// we take err address to simplify usage in defer()
	return func(err *error) {
		m.With("success", strconv.FormatBool(*err == nil)).Observe(time.Since(start).Seconds())
	}
}
