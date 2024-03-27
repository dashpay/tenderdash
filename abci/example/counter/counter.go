package counter

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/dashpay/tenderdash/abci/example/code"
	"github.com/dashpay/tenderdash/abci/types"
	tmcrypto "github.com/dashpay/tenderdash/crypto"
	tmtypes "github.com/dashpay/tenderdash/types"
)

type Application struct {
	types.BaseApplication

	hashCount                  int
	txCount                    int
	serial                     bool
	HasCoreChainLocks          bool
	CurrentCoreChainLockHeight uint32
	CoreChainLockStep          int32

	lastHeight        int64
	lastCoreChainLock tmtypes.CoreChainLock
	lastTxResults     []*types.ExecTxResult
}

func NewApplication(serial bool) *Application {
	return &Application{serial: serial, CoreChainLockStep: 1}
}

func (app *Application) InitCoreChainLock(initCoreChainHeight uint32, step int32) {
	app.CoreChainLockStep = step
	app.HasCoreChainLocks = true
	app.CurrentCoreChainLockHeight = initCoreChainHeight
	app.lastCoreChainLock = tmtypes.NewMockChainLock(app.CurrentCoreChainLockHeight)
}

func (app *Application) Info(_ context.Context, _ *types.RequestInfo) (*types.ResponseInfo, error) {
	return &types.ResponseInfo{Data: fmt.Sprintf("{\"hashes\":%v,\"txs\":%v}", app.hashCount, app.txCount)}, nil
}

func (app *Application) CheckTx(_ context.Context, req *types.RequestCheckTx) (*types.ResponseCheckTx, error) {
	if app.serial {
		if len(req.Tx) > 8 {
			return &types.ResponseCheckTx{
				Code: code.CodeTypeEncodingError,
			}, nil
		}
		tx8 := make([]byte, 8)
		copy(tx8[len(tx8)-len(req.Tx):], req.Tx)
		txValue := binary.BigEndian.Uint64(tx8)
		if txValue < uint64(app.txCount) {
			return &types.ResponseCheckTx{
				Code: code.CodeTypeBadNonce,
			}, nil
		}
	}
	return &types.ResponseCheckTx{Code: code.CodeTypeOK}, nil
}

func (app *Application) Query(_ context.Context, reqQuery *types.RequestQuery) (*types.ResponseQuery, error) {
	switch reqQuery.Path {
	case "verify-chainlock":
		return &types.ResponseQuery{Code: 0}, nil
	case "hash":
		return &types.ResponseQuery{Value: []byte(fmt.Sprintf("%v", app.hashCount))}, nil
	case "tx":
		return &types.ResponseQuery{Value: []byte(fmt.Sprintf("%v", app.txCount))}, nil
	default:
		return &types.ResponseQuery{Log: fmt.Sprintf("Invalid query path. Expected hash or tx, got %v", reqQuery.Path)}, nil
	}
}

func (app *Application) PrepareProposal(_ context.Context, req *types.RequestPrepareProposal) (*types.ResponsePrepareProposal, error) {
	app.handleRequest(req.Height, req.Txs)
	resp := types.ResponsePrepareProposal{
		AppHash:             make([]byte, tmcrypto.DefaultAppHashSize),
		CoreChainLockUpdate: app.lastCoreChainLock.ToProto(),
		TxResults:           app.lastTxResults,
		AppVersion:          1,
	}
	return &resp, nil
}

func (app *Application) ProcessProposal(_ context.Context, req *types.RequestProcessProposal) (*types.ResponseProcessProposal, error) {
	app.handleRequest(req.Height, req.Txs)
	resp := types.ResponseProcessProposal{
		AppHash:   make([]byte, tmcrypto.DefaultAppHashSize),
		Status:    types.ResponseProcessProposal_ACCEPT,
		TxResults: app.lastTxResults,
	}
	return &resp, nil
}

func (app *Application) FinalizeBlock(_ context.Context, req *types.RequestFinalizeBlock) (*types.ResponseFinalizeBlock, error) {
	app.handleRequest(req.Height, req.Block.Data.Txs)
	resp := types.ResponseFinalizeBlock{}
	app.updateCoreChainLock()
	return &resp, nil
}

func (app *Application) handleRequest(height int64, txs [][]byte) {
	if app.lastHeight == height {
		return
	}
	app.lastHeight = height
	app.lastTxResults = app.handleTxs(txs)
}

func (app *Application) handleTxs(txs [][]byte) []*types.ExecTxResult {
	var txResults []*types.ExecTxResult
	for _, tx := range txs {
		if app.serial {
			if len(tx) > 8 {
				txResults = append(txResults, &types.ExecTxResult{
					Code: code.CodeTypeEncodingError,
					Log:  fmt.Sprintf("Max tx size is 8 bytes, got %d", len(tx)),
				})
			}
			tx8 := make([]byte, 8)
			copy(tx8[len(tx8)-len(tx):], tx)
			txValue := binary.BigEndian.Uint64(tx8)
			if txValue != uint64(app.txCount) {
				txResults = append(txResults, &types.ExecTxResult{
					Code: code.CodeTypeBadNonce,
					Log:  fmt.Sprintf("Invalid nonce. Expected %v, got %v", app.txCount, txValue),
				})
			}
		}
		app.txCount++
	}
	return txResults
}

func (app *Application) updateCoreChainLock() {
	if !app.HasCoreChainLocks {
		return
	}
	app.CurrentCoreChainLockHeight += uint32(app.CoreChainLockStep)
	app.lastCoreChainLock = tmtypes.NewMockChainLock(app.CurrentCoreChainLockHeight)
}
