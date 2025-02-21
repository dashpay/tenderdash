package core

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/internal/mempool"
	"github.com/dashpay/tenderdash/internal/state/indexer"
	tmmath "github.com/dashpay/tenderdash/libs/math"
	"github.com/dashpay/tenderdash/rpc/coretypes"
	"github.com/dashpay/tenderdash/types"
)

//-----------------------------------------------------------------------------
// NOTE: tx should be signed, but this is only checked at the app level (not by Tendermint!)

// BroadcastTxAsync returns right away, with no response. Does not wait for
// CheckTx nor DeliverTx results.
// More:
// https://docs.tendermint.com/master/rpc/#/Tx/broadcast_tx_async
// Deprecated and should be removed in 0.37
func (env *Environment) BroadcastTxAsync(_ctx context.Context, req *coretypes.RequestBroadcastTx) (*coretypes.ResultBroadcastTx, error) {
	go func() {
		var (
			ctx    context.Context
			cancel context.CancelFunc
		)
		// We need to create a new context here, because the original context
		// may be canceled after parent function returns.
		if env.Config.TimeoutBroadcastTx > 0 {
			ctx, cancel = context.WithTimeout(context.Background(), env.Config.TimeoutBroadcastTx)
		} else {
			ctx, cancel = context.WithCancel(context.Background())
		}
		defer cancel()

		if res, err := env.BroadcastTx(ctx, req); err != nil || res.Code != abci.CodeTypeOK {
			env.Logger.Error("error on broadcastTxAsync", "err", err, "result", res, "tx", req.Tx.Hash())
		}
	}()

	return &coretypes.ResultBroadcastTx{Hash: req.Tx.Hash()}, nil
}

// Deprecated and should be remove in 0.37
func (env *Environment) BroadcastTxSync(ctx context.Context, req *coretypes.RequestBroadcastTx) (*coretypes.ResultBroadcastTx, error) {
	return env.BroadcastTx(ctx, req)
}

// BroadcastTx returns with the response from CheckTx. Does not wait for
// DeliverTx result.
// More: https://docs.tendermint.com/master/rpc/#/Tx/broadcast_tx_sync
func (env *Environment) BroadcastTx(ctx context.Context, req *coretypes.RequestBroadcastTx) (*coretypes.ResultBroadcastTx, error) {
	var cancel context.CancelFunc

	if env.Config.TimeoutBroadcastTx > 0 {
		ctx, cancel = context.WithTimeout(ctx, env.Config.TimeoutBroadcastTx)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	resCh := make(chan *abci.ResponseCheckTx, 1)
	err := env.Mempool.CheckTx(
		ctx,
		req.Tx,
		func(res *abci.ResponseCheckTx) {
			select {
			case <-ctx.Done():
			case resCh <- res:
			}
		},
		mempool.TxInfo{},
	)
	if err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("broadcast confirmation not received: %w", ctx.Err())
	case r := <-resCh:
		return &coretypes.ResultBroadcastTx{
			Code:      r.Code,
			Data:      r.Data,
			Codespace: r.Codespace,
			Info:      r.Info,
			Hash:      req.Tx.Hash(),
		}, nil
	}
}

// BroadcastTxCommit returns with the responses from CheckTx and DeliverTx.
// More: https://docs.tendermint.com/master/rpc/#/Tx/broadcast_tx_commit
func (env *Environment) BroadcastTxCommit(ctx context.Context, req *coretypes.RequestBroadcastTx) (*coretypes.ResultBroadcastTxCommit, error) {
	resCh := make(chan *abci.ResponseCheckTx, 1)
	err := env.Mempool.CheckTx(
		ctx,
		req.Tx,
		func(res *abci.ResponseCheckTx) {
			select {
			case <-ctx.Done():
			case resCh <- res:
			}
		},
		mempool.TxInfo{},
	)
	if err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("broadcast confirmation not received: %w", ctx.Err())
	case r := <-resCh:
		if r.Code != abci.CodeTypeOK {
			return &coretypes.ResultBroadcastTxCommit{
				CheckTx: *r,
				Hash:    req.Tx.Hash(),
			}, fmt.Errorf("wrong ABCI CodeType, got (%d) instead of OK", r.Code)
		}

		if !indexer.KVSinkEnabled(env.EventSinks) {
			return &coretypes.ResultBroadcastTxCommit{
					CheckTx: *r,
					Hash:    req.Tx.Hash(),
				},
				errors.New("cannot confirm transaction because kvEventSink is not enabled")
		}

		startAt := time.Now()
		timer := time.NewTimer(0)
		defer timer.Stop()

		count := 0
		for {
			count++
			select {
			case <-ctx.Done():
				env.Logger.Error("error on broadcastTxCommit",
					"duration", time.Since(startAt),
					"err", err)
				return &coretypes.ResultBroadcastTxCommit{
						CheckTx: *r,
						Hash:    req.Tx.Hash(),
					}, fmt.Errorf("timeout waiting for commit of tx %s (%s)",
						req.Tx.Hash(), time.Since(startAt))
			case <-timer.C:
				txres, err := env.Tx(ctx, &coretypes.RequestTx{
					Hash:  req.Tx.Hash(),
					Prove: false,
				})
				if err != nil {
					jitter := 100*time.Millisecond + time.Duration(rand.Int63n(int64(time.Second))) //nolint:gosec
					backoff := 100 * time.Duration(count) * time.Millisecond
					timer.Reset(jitter + backoff)
					continue
				}

				return &coretypes.ResultBroadcastTxCommit{
					CheckTx:  *r,
					TxResult: txres.TxResult,
					Hash:     req.Tx.Hash(),
					Height:   txres.Height,
				}, nil
			}
		}
	}
}

// UnconfirmedTxs gets unconfirmed transactions from the mempool in order of priority.
// More: https://docs.tendermint.com/master/rpc/#/Info/unconfirmed_txs
func (env *Environment) UnconfirmedTxs(_ctx context.Context, req *coretypes.RequestUnconfirmedTxs) (*coretypes.ResultUnconfirmedTxs, error) {
	totalCount := env.Mempool.Size()
	perPage := env.validatePerPage(req.PerPage.IntPtr())
	page, err := validatePage(req.Page.IntPtr(), perPage, totalCount)
	if err != nil {
		return nil, err
	}

	skipCount := validateSkipCount(page, perPage)
	// TODO: filter by tx hash here
	txs := env.Mempool.ReapMaxTxs(skipCount + tmmath.MinInt(perPage, totalCount-skipCount))
	result := txs[skipCount:]

	return &coretypes.ResultUnconfirmedTxs{
		Count:      len(result),
		Total:      totalCount,
		TotalBytes: env.Mempool.SizeBytes(),
		Txs:        result,
	}, nil
}

// return single unconfirmed transaction, matching req.TxHash
func (env *Environment) UnconfirmedTx(_ctx context.Context, req *coretypes.RequestUnconfirmedTx) (*coretypes.ResultUnconfirmedTx, error) {
	if req == nil || req.TxHash.IsZero() {
		return nil, errors.New("you mustprovide transaction hash in tx_hash")
	}

	tx := env.Mempool.GetTxByHash(types.TxKey(req.TxHash))
	if tx == nil {
		return nil, fmt.Errorf("transaction %X not found", req.TxHash)
	}

	return &coretypes.ResultUnconfirmedTx{Tx: tx}, nil
}

// NumUnconfirmedTxs gets number of unconfirmed transactions.
// More: https://docs.tendermint.com/master/rpc/#/Info/num_unconfirmed_txs
func (env *Environment) NumUnconfirmedTxs(_ctx context.Context) (*coretypes.ResultUnconfirmedTxs, error) {
	return &coretypes.ResultUnconfirmedTxs{
		Count:      env.Mempool.Size(),
		Total:      env.Mempool.Size(),
		TotalBytes: env.Mempool.SizeBytes()}, nil
}

// CheckTx checks the transaction without executing it. The transaction won't
// be added to the mempool either.
// More: https://docs.tendermint.com/master/rpc/#/Tx/check_tx
func (env *Environment) CheckTx(ctx context.Context, req *coretypes.RequestCheckTx) (*coretypes.ResultCheckTx, error) {
	res, err := env.ProxyApp.CheckTx(ctx, &abci.RequestCheckTx{Tx: req.Tx})
	if err != nil {
		return nil, err
	}
	return &coretypes.ResultCheckTx{ResponseCheckTx: *res}, nil
}

func (env *Environment) RemoveTx(_ctx context.Context, req *coretypes.RequestRemoveTx) error {
	return env.Mempool.RemoveTxByKey(req.TxKey)
}
