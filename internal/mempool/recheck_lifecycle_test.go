package mempool

import (
	"context"
	stdsync "sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/libs/log"
)

type recheckLifecycleClient struct {
	abciclient.Client
	check func(context.Context) (*abci.ResponseCheckTx, error)
	flush func(context.Context) error
}

func (c recheckLifecycleClient) CheckTx(ctx context.Context, _ *abci.RequestCheckTx) (*abci.ResponseCheckTx, error) {
	return c.check(ctx)
}

func (c recheckLifecycleClient) Flush(ctx context.Context) error { return c.flush(ctx) }

func startLifecycleRecheck(t *testing.T, mp *TxMempool) {
	t.Helper()
	mp.Lock()
	defer mp.Unlock()
	require.NoError(t, mp.Update(context.Background(), mp.height+1, nil, nil, nil, nil, true))
}

func TestStopRechecksJoinsSupersededBatches(t *testing.T) {
	entered := make(chan context.Context, 2)
	releases := []chan struct{}{make(chan struct{}), make(chan struct{})}
	unblockFirst := stdsync.OnceFunc(func() { close(releases[0]) })
	unblockSecond := stdsync.OnceFunc(func() { close(releases[1]) })
	defer unblockFirst()
	defer unblockSecond()
	var calls atomic.Int32
	client := recheckLifecycleClient{
		check: func(ctx context.Context) (*abci.ResponseCheckTx, error) {
			call := calls.Add(1)
			if call <= 2 {
				entered <- ctx
				<-releases[call-1]
			}
			return nil, context.Canceled
		},
		flush: func(context.Context) error { return nil },
	}
	mp := NewTxMempool(log.NewNopLogger(), config.DefaultMempoolConfig(), client)
	require.NoError(t, mp.addNewTransaction(randomTx(), &abci.ResponseCheckTx{Code: abci.CodeTypeOK}))
	startLifecycleRecheck(t, mp)
	first := <-entered
	startLifecycleRecheck(t, mp)
	second := <-entered
	require.ErrorIs(t, first.Err(), context.Canceled)
	done := make(chan struct{})
	go func() { mp.StopRechecks(); close(done) }()
	select {
	case <-done:
		t.Fatal("shutdown returned while application rechecks were blocked")
	case <-second.Done():
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel the active recheck")
	}
	unblockSecond()
	select {
	case <-done:
		t.Fatal("shutdown omitted the superseded recheck")
	case <-time.After(20 * time.Millisecond):
	}
	unblockFirst()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not join completed rechecks")
	}
	startLifecycleRecheck(t, mp)
	mp.StopRechecks()
	require.EqualValues(t, 2, calls.Load(), "shutdown must reject subsequent recheck admission")
}

func TestStopRechecksJoinsFlush(t *testing.T) {
	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	unblock := stdsync.OnceFunc(func() { close(release) })
	defer unblock()
	client := recheckLifecycleClient{
		check: func(context.Context) (*abci.ResponseCheckTx, error) {
			return &abci.ResponseCheckTx{Code: abci.CodeTypeOK}, nil
		},
		flush: func(ctx context.Context) error { entered <- ctx; <-release; return ctx.Err() },
	}
	mp := NewTxMempool(log.NewNopLogger(), config.DefaultMempoolConfig(), client)
	require.NoError(t, mp.addNewTransaction(randomTx(), &abci.ResponseCheckTx{Code: abci.CodeTypeOK}))
	startLifecycleRecheck(t, mp)
	ctx := <-entered
	done := make(chan struct{})
	go func() { mp.StopRechecks(); close(done) }()
	select {
	case <-done:
		t.Fatal("shutdown returned while application flush was blocked")
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel application flush")
	}
	unblock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not join application flush")
	}
}

func TestStopRechecksRacesAdmission(t *testing.T) {
	for range 32 {
		var active atomic.Int32
		client := recheckLifecycleClient{
			check: func(ctx context.Context) (*abci.ResponseCheckTx, error) {
				active.Add(1)
				defer active.Add(-1)
				<-ctx.Done()
				return nil, ctx.Err()
			},
			flush: func(context.Context) error { return nil },
		}
		mp := NewTxMempool(log.NewNopLogger(), config.DefaultMempoolConfig(), client)
		require.NoError(t, mp.addNewTransaction(randomTx(), &abci.ResponseCheckTx{Code: abci.CodeTypeOK}))
		start := make(chan struct{})
		updated := make(chan error, 1)
		stopped := make(chan struct{})
		go func() {
			<-start
			mp.Lock()
			err := mp.Update(context.Background(), 1, nil, nil, nil, nil, true)
			mp.Unlock()
			updated <- err
		}()
		go func() { <-start; mp.StopRechecks(); close(stopped) }()
		close(start)
		require.NoError(t, <-updated)
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Fatal("shutdown raced recheck admission without canceling it")
		}
		require.Zero(t, active.Load(), "an admitted application call survived shutdown")
	}
}
