package kvstore

import (
	"bytes"
	"context"
	"fmt"
	"sync"

	"github.com/gogo/protobuf/proto"
	dbm "github.com/tendermint/tm-db"

	"github.com/tendermint/tendermint/abci/example/code"
	"github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/version"
)

type App struct {
	types.BaseApplication
	mu           sync.Mutex
	state        State
	RetainBlocks int64 // blocks to retain after commit (via ResponseCommit.RetainHeight)
	logger       log.Logger

	roundAppHash        []byte
	validatorSetUpdates map[int64]types.ValidatorSetUpdate
}

func WithValidatorSetUpdates(validatorSetUpdates map[int64]types.ValidatorSetUpdate) func(app *App) {
	return func(app *App) {
		for height, vsu := range validatorSetUpdates {
			app.AddValidatorSetUpdate(vsu, height)
		}
	}
}

func New(opts ...func(app *App)) *App {
	db := dbm.NewMemDB()
	app := &App{
		logger:              log.NewNopLogger(),
		state:               loadState(db),
		validatorSetUpdates: make(map[int64]types.ValidatorSetUpdate),
	}
	for _, opt := range opts {
		opt(app)
	}
	return app
}

func (app *App) getValidatorSetUpdate(height int64) *types.ValidatorSetUpdate {
	vsu, ok := app.validatorSetUpdates[height]
	if !ok {
		var prev int64
		for h, v := range app.validatorSetUpdates {
			if h < height && prev <= h {
				vsu = v
				prev = h
			}
		}
	}
	return proto.Clone(&vsu).(*types.ValidatorSetUpdate)
}

func (app *App) Info(_ context.Context, req *types.RequestInfo) (*types.ResponseInfo, error) {
	app.mu.Lock()
	defer app.mu.Unlock()
	return &types.ResponseInfo{
		Data:             fmt.Sprintf("{\"size\":%v}", app.state.Size),
		Version:          version.ABCIVersion,
		AppVersion:       ProtocolVersion,
		LastBlockHeight:  app.state.Height,
		LastBlockAppHash: app.state.AppHash,
	}, nil
}

func (*App) CheckTx(_ context.Context, req *types.RequestCheckTx) (*types.ResponseCheckTx, error) {
	return &types.ResponseCheckTx{Code: code.CodeTypeOK, GasWanted: 1}, nil
}

func (app *App) InitChain(ctx context.Context, req *types.RequestInitChain) (*types.ResponseInitChain, error) {
	app.mu.Lock()
	defer app.mu.Unlock()
	if req.ValidatorSet != nil {
		app.validatorSetUpdates[req.InitialHeight] = *req.ValidatorSet
	}

	return &types.ResponseInitChain{}, nil
}

func (app *App) PrepareProposal(_ context.Context, req *types.RequestPrepareProposal) (*types.ResponsePrepareProposal, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	resultTxs := app.executeTxs(req.Txs)

	app.roundAppHash = app.state.appHash()

	return &types.ResponsePrepareProposal{
		TxRecords:             app.substPrepareTx(req.Txs, req.MaxTxBytes),
		AppHash:               app.roundAppHash,
		TxResults:             resultTxs,
		ConsensusParamUpdates: nil,
		CoreChainLockUpdate:   nil,
		ValidatorSetUpdate:    app.getValidatorSetUpdate(req.Height),
	}, nil
}

func (app *App) ProcessProposal(_ context.Context, req *types.RequestProcessProposal) (*types.ResponseProcessProposal, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	// Using a memdb - just return the big endian size of the db
	txResults := make([]*types.ExecTxResult, len(req.Txs))
	if app.state.AppHash != nil && !bytes.Equal(app.state.AppHash, app.roundAppHash) {
		for i := range req.Txs {
			key, _ := parseTx(req.Txs[i])
			txResults[i] = normalTxResult(key)
		}
	} else {
		// execute block
		for i, tx := range req.Txs {
			if len(tx) == 0 {
				return &types.ResponseProcessProposal{Status: types.ResponseProcessProposal_REJECT}, nil
			}
			txResults[i] = app.handleTx(tx)
		}
		app.roundAppHash = app.state.appHash()
	}
	return &types.ResponseProcessProposal{
		Status:             types.ResponseProcessProposal_ACCEPT,
		AppHash:            app.roundAppHash,
		TxResults:          txResults,
		ValidatorSetUpdate: app.getValidatorSetUpdate(req.Height),
	}, nil
}

func (app *App) FinalizeBlock(_ context.Context, req *types.RequestFinalizeBlock) (*types.ResponseFinalizeBlock, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	// Using a memdb - just return the big endian size of the db
	txResults := make([]*types.ExecTxResult, len(req.Txs))
	if app.state.AppHash != nil && !bytes.Equal(app.state.AppHash, app.roundAppHash) {
		for i := range req.Txs {
			key, _ := parseTx(req.Txs[i])
			txResults[i] = normalTxResult(key)
		}
	} else {
		// execute block
		for i, tx := range req.Txs {
			if len(tx) == 0 {
				return &types.ResponseFinalizeBlock{}, nil
			}
			txResults[i] = app.handleTx(tx)
		}
		app.roundAppHash = app.state.appHash()
	}
	return &types.ResponseFinalizeBlock{}, nil
}

func (app *App) Close() error {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.state.db.Close()
}

func (app *App) Commit(_ context.Context) (*types.ResponseCommit, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	app.state.AppHash = app.roundAppHash
	app.state.Height++
	saveState(app.state)

	resp := &types.ResponseCommit{Data: app.state.AppHash}
	if app.RetainBlocks > 0 && app.state.Height >= app.RetainBlocks {
		resp.RetainHeight = app.state.Height - app.RetainBlocks + 1
	}
	return resp, nil
}

// Query returns an associated value or nil if missing.
func (app *App) Query(_ context.Context, reqQuery *types.RequestQuery) (*types.ResponseQuery, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	switch reqQuery.Path {
	case "/verify-chainlock":
		return &types.ResponseQuery{
			Code: 0,
		}, nil
	}

	if reqQuery.Prove {
		value, err := app.state.db.Get(prefixKey(reqQuery.Data))
		if err != nil {
			panic(err)
		}

		resQuery := types.ResponseQuery{
			Index:  -1,
			Key:    reqQuery.Data,
			Value:  value,
			Height: app.state.Height,
		}

		if value == nil {
			resQuery.Log = "does not exist"
		} else {
			resQuery.Log = "exists"
		}

		return &resQuery, nil
	}

	value, err := app.state.db.Get(prefixKey(reqQuery.Data))
	if err != nil {
		panic(err)
	}

	resQuery := types.ResponseQuery{
		Key:    reqQuery.Data,
		Value:  value,
		Height: app.state.Height,
	}

	if value == nil {
		resQuery.Log = "does not exist"
	} else {
		resQuery.Log = "exists"
	}

	return &resQuery, nil
}

func (app *App) executeTxs(txs [][]byte) []*types.ExecTxResult {
	txResults := make([]*types.ExecTxResult, len(txs))
	for i, tx := range txs {
		txResults[i] = app.handleTx(tx)
	}
	return txResults
}

// tx is either "val:pubkey!power" or "key=value" or just arbitrary bytes
func (app *App) handleTx(tx []byte) *types.ExecTxResult {
	if isPrepareTx(tx) {
		return app.execPrepareTx(tx)
	}

	key, value := parseTx(tx)
	err := app.state.db.Set(prefixKey([]byte(key)), []byte(value))
	if err != nil {
		panic(err)
	}
	return normalTxResult(key)
}

// -----------------------------
// prepare proposal machinery

// execPrepareTx is noop. tx data is considered as placeholder
// and is substitute at the PrepareProposal.
func (app *App) execPrepareTx(tx []byte) *types.ExecTxResult {
	// noop
	return &types.ExecTxResult{}
}

// substPrepareTx substitutes all the transactions prefixed with 'prepare' in the
// proposal for transactions with the prefix stripped.
// It marks all of the original transactions as 'REMOVED' so that
// Tendermint will remove them from its mempool.
func (app *App) substPrepareTx(blockData [][]byte, maxTxBytes int64) []*types.TxRecord {
	trs := make([]*types.TxRecord, 0, len(blockData))
	var removed []*types.TxRecord
	var totalBytes int64
	for _, tx := range blockData {
		txMod := tx
		action := types.TxRecord_UNMODIFIED
		if isPrepareTx(tx) {
			removed = append(removed, &types.TxRecord{
				Tx:     tx,
				Action: types.TxRecord_REMOVED,
			})
			txMod = bytes.TrimPrefix(tx, []byte(PreparePrefix))
			action = types.TxRecord_ADDED
		}
		totalBytes += int64(len(txMod))
		if totalBytes > maxTxBytes {
			break
		}
		trs = append(trs, &types.TxRecord{
			Tx:     txMod,
			Action: action,
		})
	}

	return append(trs, removed...)
}

// AddValidatorSetUpdate ...
func (app *App) AddValidatorSetUpdate(vsu types.ValidatorSetUpdate, height int64) {
	app.mu.Lock()
	defer app.mu.Unlock()
	app.validatorSetUpdates[height] = vsu
}
