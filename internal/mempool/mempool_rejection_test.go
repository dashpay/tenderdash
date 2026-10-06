package mempool

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

// scriptedApp answers every CheckTx with a configurable response and counts the calls.
type scriptedApp struct {
	abci.BaseApplication
	response atomic.Pointer[abci.ResponseCheckTx]
	calls    atomic.Int32
}

func (app *scriptedApp) CheckTx(context.Context, *abci.RequestCheckTx) (*abci.ResponseCheckTx, error) {
	app.calls.Add(1)
	rsp := *app.response.Load()
	return &rsp, nil
}

func setupScripted(
	ctx context.Context,
	t *testing.T,
	keepInvalid bool,
	rsp *abci.ResponseCheckTx,
) (*TxMempool, *scriptedApp) {
	t.Helper()

	app := &scriptedApp{}
	app.response.Store(rsp)

	client := abciclient.NewLocalClient(log.NewNopLogger(), app)
	require.NoError(t, client.Start(ctx))
	t.Cleanup(client.Wait)

	txmp := setup(t, client, 100)
	txmp.config.KeepInvalidTxsInCache = keepInvalid

	return txmp, app
}

// submit runs CheckTx and returns the response passed to the callback, if any.
func submit(ctx context.Context, txmp *TxMempool, tx types.Tx, txInfo TxInfo) (*abci.ResponseCheckTx, error) {
	var got *abci.ResponseCheckTx
	err := txmp.CheckTx(ctx, tx, func(rsp *abci.ResponseCheckTx) { got = rsp }, txInfo)
	return got, err
}

func rejection() *abci.ResponseCheckTx {
	return &abci.ResponseCheckTx{
		Code:      7,
		Codespace: "app",
		Info:      "insufficient balance",
		Data:      []byte{0xde, 0xad},
		GasWanted: 1,
	}
}

func TestTxMempool_ResubmitRejectedTx(t *testing.T) {
	const peerID uint16 = 1

	testCases := []struct {
		name         string
		keepInvalid  bool
		first        *abci.ResponseCheckTx
		senderID     uint16
		senderNodeID types.NodeID
		wantErr      error
		wantReplay   bool
		wantCalls    int32
	}{
		{
			name:        "local caller gets the stored rejection",
			keepInvalid: true,
			first:       rejection(),
			senderID:    UnknownPeerID,
			wantReplay:  true,
			wantCalls:   1,
		},
		{
			name:        "peer still gets ErrTxInCache",
			keepInvalid: true,
			first:       rejection(),
			senderID:    peerID,
			wantErr:     types.ErrTxInCache,
			wantCalls:   1,
		},
		{
			name:         "peer without a reserved ID still gets ErrTxInCache",
			keepInvalid:  true,
			first:        rejection(),
			senderID:     UnknownPeerID,
			senderNodeID: "peer",
			wantErr:      types.ErrTxInCache,
			wantCalls:    1,
		},
		{
			name:        "oversized rejection is not stored",
			keepInvalid: true,
			first: &abci.ResponseCheckTx{
				Code: 7,
				Info: strings.Repeat("x", maxCachedRejectionBytes+1),
			},
			senderID:  UnknownPeerID,
			wantErr:   types.ErrTxInCache,
			wantCalls: 1,
		},
		{
			name:        "accepted transaction still gets ErrTxInCache",
			keepInvalid: true,
			first:       &abci.ResponseCheckTx{Code: abci.CodeTypeOK, GasWanted: 1},
			senderID:    UnknownPeerID,
			wantErr:     types.ErrTxInCache,
			wantCalls:   1,
		},
		{
			name:        "rejected transaction is checked again when not kept in cache",
			keepInvalid: false,
			first:       rejection(),
			senderID:    UnknownPeerID,
			wantReplay:  true,
			wantCalls:   2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			txmp, app := setupScripted(ctx, t, tc.keepInvalid, tc.first)
			tx := types.Tx("sender=key=1")

			got, err := submit(ctx, txmp, tx, TxInfo{})
			require.NoError(t, err)
			require.Equal(t, tc.first.Code, got.Code)

			got, err = submit(ctx, txmp, tx, TxInfo{SenderID: tc.senderID, SenderNodeID: tc.senderNodeID})
			require.ErrorIs(t, err, tc.wantErr)
			assert.Equal(t, tc.wantCalls, app.calls.Load())

			if !tc.wantReplay {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tc.first.Code, got.Code)
			assert.Equal(t, tc.first.Codespace, got.Codespace)
			assert.Equal(t, tc.first.Info, got.Info)
			assert.Equal(t, tc.first.Data, got.Data)
		})
	}
}

func TestTxMempool_RejectedThenCommittedTx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	txmp, _ := setupScripted(ctx, t, true, rejection())
	tx := types.Tx("sender=key=1")

	_, err := submit(ctx, txmp, tx, TxInfo{})
	require.NoError(t, err)

	// Another proposer includes the transaction and it executes successfully.
	txmp.Lock()
	err = txmp.Update(ctx, 1, types.Txs{tx}, []*abci.ExecTxResult{{Code: abci.CodeTypeOK}}, nil, nil, false)
	txmp.Unlock()
	require.NoError(t, err)

	got, err := submit(ctx, txmp, tx, TxInfo{})
	require.ErrorIs(t, err, types.ErrTxInCache)
	assert.Nil(t, got)
}

// The block containing the transaction is committed after the application
// rejected it in CheckTx, but before the mempool recorded that rejection.
func TestTxMempool_CommittedWhileCheckTxInFlight(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	txmp, _ := setupScripted(ctx, t, true, rejection())
	tx := types.Tx("sender=key=1")

	commit := func(*abci.ResponseCheckTx) {
		txmp.Lock()
		defer txmp.Unlock()
		err := txmp.Update(ctx, 1, types.Txs{tx}, []*abci.ExecTxResult{{Code: abci.CodeTypeOK}}, nil, nil, false)
		assert.NoError(t, err)
	}
	require.NoError(t, txmp.CheckTx(ctx, tx, commit, TxInfo{}))

	got, err := submit(ctx, txmp, tx, TxInfo{})
	require.ErrorIs(t, err, types.ErrTxInCache)
	assert.Nil(t, got)
}

// The cache is smaller than the mempool, so a pending transaction can lose its
// cache entry and be checked, and rejected, a second time.
func TestTxMempool_PendingTxRejectedOnDuplicateCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	txmp, app := setupScripted(ctx, t, true, &abci.ResponseCheckTx{Code: abci.CodeTypeOK, GasWanted: 1})
	txmp.cache = NewLRUTxCache(1)
	pending, other := types.Tx("sender-1=key=1"), types.Tx("sender-2=key=2")

	_, err := submit(ctx, txmp, pending, TxInfo{})
	require.NoError(t, err)
	require.Equal(t, 1, txmp.Size())

	app.response.Store(rejection())
	_, err = submit(ctx, txmp, other, TxInfo{})
	require.NoError(t, err)
	_, err = submit(ctx, txmp, pending, TxInfo{})
	require.NoError(t, err)
	require.Equal(t, 1, txmp.Size())
	require.Equal(t, int32(3), app.calls.Load())

	got, err := submit(ctx, txmp, pending, TxInfo{})
	require.ErrorIs(t, err, types.ErrTxInCache)
	assert.Nil(t, got)
}

func TestTxMempool_ResubmitTxRejectedOnRecheck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	txmp, app := setupScripted(ctx, t, true, &abci.ResponseCheckTx{Code: abci.CodeTypeOK, GasWanted: 1})
	tx := types.Tx("sender=key=1")

	_, err := submit(ctx, txmp, tx, TxInfo{})
	require.NoError(t, err)
	require.Equal(t, 1, txmp.Size())

	txmp.handleRecheckResult(tx, rejection())
	require.Equal(t, 0, txmp.Size())

	got, err := submit(ctx, txmp, tx, TxInfo{})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, rejection().Code, got.Code)
	assert.Equal(t, rejection().Info, got.Info)
	assert.Equal(t, int32(1), app.calls.Load())
}
