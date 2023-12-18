package kvstore

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/dashpay/tenderdash/abci/example/code"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/types"
)

// PrepareTxsFunc prepares transactions, possibly adding and/or removing some of them
type PrepareTxsFunc func(req abci.RequestPrepareProposal) ([]*abci.TxRecord, error)

// VerifyTxFunc checks if transaction is correct
type VerifyTxFunc func(tx types.Tx, typ abci.CheckTxType) (abci.ResponseCheckTx, error)

// ExecTxFunc executes the transaction against some state
type ExecTxFunc func(tx types.Tx, roundState State) (abci.ExecTxResult, error)

// Helper struct that controls size of added transactions
type TxRecords struct {
	Size  int64
	Limit int64
	Txs   []*abci.TxRecord
}

// Add new transaction if it fits the limit.
//
// Returns action that was taken, as some transactions may be delayed despite provided `tx.Action`.
// Errors when newly added transaction does not fit the limit.
func (txr *TxRecords) Add(tx *abci.TxRecord) (abci.TxRecord_TxAction, error) {
	txSize := int64(len(tx.Tx))
	switch tx.Action {
	case abci.TxRecord_ADDED:
		if txr.Size+txSize > txr.Limit {
			return abci.TxRecord_UNKNOWN, errors.New("new transaction cannot be added: over limit")
		}
		// add to txs
		txr.Txs = append(txr.Txs, tx)
		txr.Size += txSize
		return tx.Action, nil
	case abci.TxRecord_UNMODIFIED:
		{
			if txr.Size+txSize > txr.Limit {
				// over limit, delaying
				delay := abci.TxRecord{Tx: tx.Tx, Action: abci.TxRecord_DELAYED}
				return txr.Add(&delay)
			}

			// add to txs
			txr.Txs = append(txr.Txs, tx)
			txr.Size += txSize
			return tx.Action, nil
		}

	case abci.TxRecord_REMOVED, abci.TxRecord_DELAYED:
		// remove from txs, not counted in size
		txr.Txs = append(txr.Txs, tx)
		return tx.Action, nil
	default:
		panic(fmt.Sprintf("unknown tx action: %v", tx.Action))
	}
}

func prepareTxs(req abci.RequestPrepareProposal) ([]*abci.TxRecord, error) {
	return substPrepareTx(req.Txs, req.MaxTxBytes)
}

func txRecords2Txs(txRecords []*abci.TxRecord) types.Txs {
	txRecordSet := types.NewTxRecordSet(txRecords)
	return txRecordSet.IncludedTxs()
}

func verifyTx(tx types.Tx, _ abci.CheckTxType) (abci.ResponseCheckTx, error) {
	_, _, err := parseTx(tx)
	if err != nil {
		return abci.ResponseCheckTx{
			Code: code.CodeTypeEncodingError,
			Data: []byte(err.Error()),
		}, nil
	}

	return abci.ResponseCheckTx{Code: code.CodeTypeOK, GasWanted: 1}, nil
}

// tx is either "val:pubkey!power" or "key=value" or just arbitrary bytes
func execTx(tx types.Tx, roundState State) (abci.ExecTxResult, error) {
	if isPrepareTx(tx) {
		return execPrepareTx(tx)
	}

	key, value, err := parseTx(tx)
	if err != nil {
		err = fmt.Errorf("malformed transaction %X in ProcessProposal: %w", tx, err)
		return abci.ExecTxResult{Code: code.CodeTypeUnknownError, Log: err.Error()}, err
	}

	err = roundState.Set([]byte(key), []byte(value))
	if err != nil {
		return abci.ExecTxResult{Code: code.CodeTypeUnknownError, Log: err.Error()}, err
	}

	events := []abci.Event{
		{
			Type: "app",
			Attributes: []abci.EventAttribute{
				{Key: "creator", Value: "Cosmoshi Netowoko", Index: true},
				{Key: "key", Value: key, Index: true},
				{Key: "index_key", Value: "index is working", Index: true},
				{Key: "noindex_key", Value: "index is working", Index: false},
			},
		},
	}

	return abci.ExecTxResult{Code: code.CodeTypeOK, Events: events}, nil
}

// execPrepareTx is noop. tx data is considered as placeholder
// and is substitute at the PrepareProposal.
func execPrepareTx(tx []byte) (abci.ExecTxResult, error) {
	// noop
	return abci.ExecTxResult{}, nil
}

// substPrepareTx substitutes all the transactions prefixed with 'prepare' in the
// proposal for transactions with the prefix stripped.
// It marks all of the original transactions as 'REMOVED' so that
// Tendermint will remove them from its mempool.
func substPrepareTx(txs [][]byte, maxTxBytes int64) ([]*abci.TxRecord, error) {
	trs := TxRecords{
		Size:  0,
		Limit: maxTxBytes,
		Txs:   make([]*abci.TxRecord, 0, len(txs)+1),
	}

	for _, tx := range txs {
		action := abci.TxRecord_UNMODIFIED

		// As a special logic of this app, we replace tx with the prefix 'prepare' with one without the prefix.
		// We need to preserve ordering of the transactions.
		if isPrepareTx(tx) {
			// add new tx in place of the old one
			record := abci.TxRecord{
				Tx:     bytes.TrimPrefix(tx, []byte(PreparePrefix)),
				Action: abci.TxRecord_ADDED,
			}
			if _, err := trs.Add(&record); err != nil {
				// cannot add new tx, so we cannot remove old one - just delay it and retry next time
				action = abci.TxRecord_DELAYED
			} else {
				// old one can be removed from the mempool
				action = abci.TxRecord_REMOVED
			}
		}

		// Now we add the transaction to the list of transactions
		transaction := &abci.TxRecord{
			Tx:     tx,
			Action: action,
		}
		if _, err := trs.Add(transaction); err != nil {
			// this should definitely not fail, as we don't add anything new
			return nil, err
		}
	}

	return trs.Txs, nil
}

const PreparePrefix = "prepare"

func isPrepareTx(tx []byte) bool {
	return bytes.HasPrefix(tx, []byte(PreparePrefix))
}

// parseTx parses a tx in 'key=value' format into a key and value.
func parseTx(tx types.Tx) (string, string, error) {
	parts := bytes.SplitN(tx, []byte("="), 2)
	switch len(parts) {
	case 0:
		return "", "", errors.New("key cannot be empty")
	case 1:
		return string(parts[0]), string(parts[0]), nil
	case 2:
		return string(parts[0]), string(parts[1]), nil
	default:
		return "", "", fmt.Errorf("invalid tx format: %q", string(tx))
	}
}
