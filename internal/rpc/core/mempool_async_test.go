package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/mempool"
	mempoolmocks "github.com/dashpay/tenderdash/internal/mempool/mocks"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/rpc/coretypes"
	rpcserver "github.com/dashpay/tenderdash/rpc/jsonrpc/server"
	rpctypes "github.com/dashpay/tenderdash/rpc/jsonrpc/types"
	"github.com/dashpay/tenderdash/types"
)

func asyncTestEnvironment(t *testing.T, cfg config.RPCConfig, check func(context.Context, types.Tx, func(*abci.ResponseCheckTx)) error) *Environment {
	t.Helper()
	mp := mempoolmocks.NewMempool(t)
	mp.EXPECT().CheckTx(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, tx types.Tx, cb func(*abci.ResponseCheckTx), _ mempool.TxInfo) error {
			return check(ctx, tx, cb)
		}).Maybe()
	env := &Environment{Config: cfg, Mempool: mp, Logger: log.NewNopLogger()}
	require.NoError(t, env.StartAsyncBroadcasts(t.Context()))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, env.StopAsyncBroadcasts(ctx))
	})
	return env
}

func TestBroadcastTxAsyncDefaultLimit(t *testing.T) {
	for _, limit := range []int{0, 100, 2} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			cfg := *config.DefaultRPCConfig()
			cfg.MaxConcurrentBroadcastTxAsync = limit
			env := asyncTestEnvironment(t, cfg, func(ctx context.Context, _ types.Tx, _ func(*abci.ResponseCheckTx)) error {
				<-ctx.Done()
				return ctx.Err()
			})
			if limit == 0 {
				limit = 100
			}
			for i := range limit {
				tx := types.Tx{byte(i)}
				res, err := env.BroadcastTxAsync(t.Context(), &coretypes.RequestBroadcastTx{Tx: tx})
				require.NoError(t, err)
				require.Equal(t, tx.Hash(), res.Hash)
			}
			res, err := env.BroadcastTxAsync(t.Context(), &coretypes.RequestBroadcastTx{Tx: types.Tx("overflow")})
			require.ErrorIs(t, err, coretypes.ErrTooManyRequests)
			require.Nil(t, res)
		})
	}
}

func TestBroadcastTxAsyncCanceledRequest(t *testing.T) {
	env := asyncTestEnvironment(t, *config.DefaultRPCConfig(), func(context.Context, types.Tx, func(*abci.ResponseCheckTx)) error {
		t.Error("canceled request reached CheckTx")
		return context.Canceled
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	res, err := env.BroadcastTxAsync(ctx, &coretypes.RequestBroadcastTx{Tx: types.Tx("canceled")})
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, res)
}

func TestBroadcastTxAsyncRequestIndependent(t *testing.T) {
	entered := make(chan context.Context, 1)
	readTx := make(chan struct{})
	checkedTx := make(chan types.Tx, 1)
	env := asyncTestEnvironment(t, *config.DefaultRPCConfig(), func(ctx context.Context, tx types.Tx, cb func(*abci.ResponseCheckTx)) error {
		entered <- ctx
		select {
		case <-readTx:
			checkedTx <- tx
			cb(&abci.ResponseCheckTx{})
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req := &coretypes.RequestBroadcastTx{Tx: types.Tx("original")}
	res, err := env.BroadcastTxAsync(ctx, req)
	require.NoError(t, err)
	require.Equal(t, req.Tx.Hash(), res.Hash)
	var jobCtx context.Context
	select {
	case jobCtx = <-entered:
	case <-time.After(time.Second):
		t.Fatal("CheckTx did not start")
	}
	cancel()
	require.NoError(t, jobCtx.Err())
	_, deadline := jobCtx.Deadline()
	require.False(t, deadline, "zero timeout must not impose a deadline")
	req.Tx[0] = 'X'
	req.Tx = types.Tx("replacement")
	close(readTx)
	select {
	case tx := <-checkedTx:
		require.Equal(t, types.Tx("original"), tx)
	case <-time.After(time.Second):
		t.Fatal("CheckTx did not read transaction")
	}
}

func TestBroadcastTxAsyncReleasesCapacity(t *testing.T) {
	for _, outcome := range []string{"success", "rejected", "error", "timeout"} {
		t.Run(outcome, func(t *testing.T) {
			cfg := *config.DefaultRPCConfig()
			cfg.MaxConcurrentBroadcastTxAsync = 1
			if outcome == "timeout" {
				cfg.TimeoutBroadcastTx = 10 * time.Millisecond
			}
			env := asyncTestEnvironment(t, cfg, func(ctx context.Context, _ types.Tx, cb func(*abci.ResponseCheckTx)) error {
				switch outcome {
				case "error":
					return errors.New("application unavailable")
				case "timeout":
					<-ctx.Done()
					return ctx.Err()
				case "rejected":
					cb(&abci.ResponseCheckTx{Code: 1})
				default:
					cb(&abci.ResponseCheckTx{})
				}
				return nil
			})
			_, err := env.BroadcastTxAsync(t.Context(), &coretypes.RequestBroadcastTx{Tx: types.Tx("first")})
			require.NoError(t, err)
			require.Eventually(t, func() bool {
				_, err := env.BroadcastTxAsync(t.Context(), &coretypes.RequestBroadcastTx{Tx: types.Tx("second")})
				return err == nil
			}, time.Second, time.Millisecond)
		})
	}
}

func TestBroadcastTxAsyncHoldsCapacityUntilCallReturns(t *testing.T) {
	cfg := *config.DefaultRPCConfig()
	cfg.MaxConcurrentBroadcastTxAsync = 1
	cfg.TimeoutBroadcastTx = 10 * time.Millisecond
	canceled := make(chan struct{})
	release := make(chan struct{})
	env := asyncTestEnvironment(t, cfg, func(ctx context.Context, _ types.Tx, _ func(*abci.ResponseCheckTx)) error {
		<-ctx.Done()
		close(canceled)
		<-release
		return ctx.Err()
	})
	t.Cleanup(func() { close(release) })
	_, err := env.BroadcastTxAsync(t.Context(), &coretypes.RequestBroadcastTx{Tx: types.Tx("stalled")})
	require.NoError(t, err)
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("CheckTx context did not expire")
	}
	_, err = env.BroadcastTxAsync(t.Context(), &coretypes.RequestBroadcastTx{Tx: types.Tx("overflow")})
	require.ErrorIs(t, err, coretypes.ErrTooManyRequests)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, env.StopAsyncBroadcasts(ctx), context.DeadlineExceeded)
	_, err = env.BroadcastTxAsync(t.Context(), &coretypes.RequestBroadcastTx{Tx: types.Tx("after stop")})
	require.ErrorIs(t, err, context.Canceled)
}

func TestBroadcastTxAsyncConcurrentAdmission(t *testing.T) {
	cfg := *config.DefaultRPCConfig()
	cfg.MaxConcurrentBroadcastTxAsync = 4
	env := asyncTestEnvironment(t, cfg, func(ctx context.Context, _ types.Tx, _ func(*abci.ResponseCheckTx)) error {
		<-ctx.Done()
		return ctx.Err()
	})
	results := make(chan error, 64)
	var wg sync.WaitGroup
	for range cap(results) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := env.BroadcastTxAsync(t.Context(), &coretypes.RequestBroadcastTx{Tx: types.Tx("tx")})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else {
			require.ErrorIs(t, err, coretypes.ErrTooManyRequests)
		}
	}
	require.Equal(t, 4, accepted)
}

func TestBroadcastTxAsyncShutdown(t *testing.T) {
	for _, stopExplicitly := range []bool{false, true} {
		t.Run(strconv.FormatBool(stopExplicitly), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			env := &Environment{Config: *config.DefaultRPCConfig(), Logger: log.NewNopLogger()}
			require.NoError(t, env.StartAsyncBroadcasts(ctx))
			if stopExplicitly {
				require.NoError(t, env.StopAsyncBroadcasts(t.Context()))
			} else {
				cancel()
			}
			res, err := env.BroadcastTxAsync(t.Context(), &coretypes.RequestBroadcastTx{Tx: types.Tx("stopped")})
			require.ErrorIs(t, err, context.Canceled)
			require.Nil(t, res)
			require.NoError(t, env.StopAsyncBroadcasts(t.Context()))
		})
	}
}

func TestBroadcastTxAsyncUninitialized(t *testing.T) {
	env := &Environment{}
	res, err := env.BroadcastTxAsync(t.Context(), &coretypes.RequestBroadcastTx{Tx: types.Tx("tx")})
	require.Error(t, err)
	require.Nil(t, res)
	require.NoError(t, env.StopAsyncBroadcasts(t.Context()))
}

func TestBroadcastTxAsyncStopIdleWithCanceledContext(t *testing.T) {
	env := &Environment{Config: *config.DefaultRPCConfig()}
	require.NoError(t, env.StartAsyncBroadcasts(t.Context()))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for range 2 {
		require.NoError(t, env.StopAsyncBroadcasts(ctx))
	}
}

func TestBroadcastTxAsyncConcurrentStopAfterTimeout(t *testing.T) {
	cfg := *config.DefaultRPCConfig()
	cfg.MaxConcurrentBroadcastTxAsync = 2
	entered := make(chan struct{})
	finish := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(finish) })
	env := asyncTestEnvironment(t, cfg, func(ctx context.Context, _ types.Tx, _ func(*abci.ResponseCheckTx)) error {
		close(entered)
		<-finish
		return ctx.Err()
	})
	t.Cleanup(unblock)
	_, err := env.BroadcastTxAsync(t.Context(), &coretypes.RequestBroadcastTx{Tx: types.Tx("stalled")})
	require.NoError(t, err)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("CheckTx did not start")
	}
	stopCtx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, env.StopAsyncBroadcasts(stopCtx), context.DeadlineExceeded)
	_, err = env.BroadcastTxAsync(t.Context(), &coretypes.RequestBroadcastTx{Tx: types.Tx("after stop")})
	require.ErrorIs(t, err, context.Canceled)

	ctx, cancelStops := context.WithTimeout(t.Context(), time.Second)
	defer cancelStops()
	results := make(chan error, 8)
	for range cap(results) {
		go func() { results <- env.StopAsyncBroadcasts(ctx) }()
	}
	unblock()
	for range cap(results) {
		require.NoError(t, <-results)
	}
	require.NoError(t, env.StopAsyncBroadcasts(stopCtx))
}

func TestBroadcastTxAsyncAdmissionDuringShutdown(t *testing.T) {
	env := asyncTestEnvironment(t, *config.DefaultRPCConfig(), func(ctx context.Context, _ types.Tx, _ func(*abci.ResponseCheckTx)) error {
		<-ctx.Done()
		return ctx.Err()
	})
	start := make(chan struct{})
	results := make(chan error, 64)
	var wg sync.WaitGroup
	for range cap(results) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := env.BroadcastTxAsync(t.Context(), &coretypes.RequestBroadcastTx{Tx: types.Tx("tx")})
			results <- err
		}()
	}
	close(start)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	require.NoError(t, env.StopAsyncBroadcasts(ctx))
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			require.ErrorIs(t, err, context.Canceled)
		}
	}
	res, err := env.BroadcastTxAsync(t.Context(), &coretypes.RequestBroadcastTx{Tx: types.Tx("after stop")})
	require.Nil(t, res)
	require.ErrorIs(t, err, context.Canceled)
}

func TestBroadcastTxAsyncHTTPBatchLimit(t *testing.T) {
	cfg := *config.DefaultRPCConfig()
	cfg.MaxConcurrentBroadcastTxAsync = 1
	env := asyncTestEnvironment(t, cfg, func(ctx context.Context, _ types.Tx, _ func(*abci.ResponseCheckTx)) error {
		<-ctx.Done()
		return ctx.Err()
	})
	mux := http.NewServeMux()
	rpcserver.RegisterRPCFuncs(mux, map[string]*rpcserver.RPCFunc{
		"broadcast_tx_async": rpcserver.NewRPCFunc(env.BroadcastTxAsync),
	}, log.NewNopLogger())
	server := httptest.NewServer(mux)
	defer server.Close()
	body := `[{"jsonrpc":"2.0","id":1,"method":"broadcast_tx_async","params":{"tx":"YQ=="}},
	{"jsonrpc":"2.0","id":2,"method":"broadcast_tx_async","params":{"tx":"Yg=="}}]`
	response, err := server.Client().Post(server.URL, "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer response.Body.Close()
	var results []rpctypes.RPCResponse
	require.NoError(t, json.NewDecoder(response.Body).Decode(&results))
	require.Len(t, results, 2)
	require.Nil(t, results[0].Error)
	require.NotNil(t, results[1].Error)
	require.EqualValues(t, rpctypes.CodeTooManyRequests, results[1].Error.Code)
	_, err = env.BroadcastTxAsync(t.Context(), &coretypes.RequestBroadcastTx{Tx: types.Tx("local")})
	require.ErrorIs(t, err, coretypes.ErrTooManyRequests)
}
