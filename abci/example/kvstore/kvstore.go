package kvstore

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	"github.com/gogo/protobuf/proto"
	dbm "github.com/tendermint/tm-db"

	"github.com/tendermint/tendermint/abci/example/code"
	"github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/crypto"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/version"
)

var (
	stateKey        = []byte("stateKey")
	kvPairPrefixKey = []byte("kvPairKey:")

	ProtocolVersion uint64 = 0x1
)

type State struct {
	db      dbm.DB
	Height  int64            `json:"height"`
	AppHash tmbytes.HexBytes `json:"app_hash"`
}

func (s *State) appHash() []byte {
	stats := s.db.Stats()
	appHash := make([]byte, crypto.DefaultAppHashSize)
	size, err := strconv.Atoi(stats["database.size"])
	if err != nil {
		panic(err)
	}
	binary.PutVarint(appHash, int64(size))
	return appHash
}

func loadState(db dbm.DB) State {
	var state State
	state.db = db
	stateBytes, err := db.Get(stateKey)
	if err != nil {
		panic(err)
	}
	if len(stateBytes) == 0 {
		return state
	}
	err = json.Unmarshal(stateBytes, &state)
	if err != nil {
		panic(err)
	}
	return state
}

func saveState(state State) {
	stateBytes, err := json.Marshal(state)
	if err != nil {
		panic(err)
	}
	err = state.db.Set(stateKey, stateBytes)
	if err != nil {
		panic(err)
	}
}

func prefixKey(key []byte) []byte {
	return append(kvPairPrefixKey, key...)
}

func (s State) Size() int64 {
	stats := s.db.Stats()
	sizeStr, ok := stats["database.size"]
	if !ok {
		return 0
	}
	size, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		panic("database size error: " + err.Error())
	}
	return size
}

// Copy copies the state. It ensures copy is a valid, initialized state.
// Caller should close the state once it's not needed anymore
// newDBfunc can be provided to define DB that will be used for this copy.
func (s State) Copy(dst *State) error {
	dst.Height = s.Height
	dst.AppHash = s.AppHash.Copy()
	// apphash is required, and should never be nil,zero-length
	if len(dst.AppHash) == 0 {
		dst.AppHash = make(tmbytes.HexBytes, crypto.DefaultAppHashSize)
	}

	dstBatch := dst.db.NewBatch()
	defer dstBatch.Close()

	// cleanup dest DB first
	dstIter, err := dst.db.Iterator(nil, nil)
	if err != nil {
		return fmt.Errorf("cannot create dest db iterator: %w", err)
	}
	defer dstIter.Close()

	keys := make([][]byte, 0, s.Size())
	for dstIter.Valid() {
		keys = append(keys, dstIter.Key())
		dstIter.Next()
	}
	for _, key := range keys {
		dstBatch.Delete(key)
	}

	// write source to dest
	if s.db != nil {
		srcIter, err := s.db.Iterator(nil, nil)
		if err != nil {
			return fmt.Errorf("cannot copy current DB: %w", err)
		}
		defer srcIter.Close()

		for srcIter.Valid() {
			if err = dstBatch.Set(srcIter.Key(), srcIter.Value()); err != nil {
				return err
			}
			srcIter.Next()
		}

		if err = dstBatch.Write(); err != nil {
			return fmt.Errorf("cannot close dest batch: %w", err)
		}
	}

	return nil
}

func (s *State) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

//---------------------------------------------------

var _ types.Application = (*Application)(nil)

type Application struct {
	types.BaseApplication
	mu sync.Mutex

	lastCommittedState State
	// roundStates contains state for each round, indexed by AppHash.String()
	roundStates  map[string]State
	RetainBlocks int64 // blocks to retain after commit (via ResponseCommit.RetainHeight)
	logger       log.Logger

	finalizedAppHash    []byte
	validatorSetUpdates map[int64]types.ValidatorSetUpdate
}

func WithValidatorSetUpdates(validatorSetUpdates map[int64]types.ValidatorSetUpdate) func(app *Application) {
	return func(app *Application) {
		for height, vsu := range validatorSetUpdates {
			app.AddValidatorSetUpdate(vsu, height)
		}
	}
}

func NewApplication(opts ...func(app *Application)) *Application {
	db := dbm.NewMemDB()
	logger, err := log.NewDefaultLogger(log.LogFormatJSON, log.LogLevelDebug)
	if err != nil {
		panic("cannot create logger: " + err.Error())
	}
	app := &Application{
		logger:              logger.With("module", "kvstore"),
		lastCommittedState:  loadState(db),
		roundStates:         map[string]State{},
		validatorSetUpdates: make(map[int64]types.ValidatorSetUpdate),
	}

	app.newHeight(0, make([]byte, crypto.DefaultAppHashSize))
	for _, opt := range opts {
		opt(app)
	}
	return app
}

func (app *Application) InitChain(_ context.Context, req *types.RequestInitChain) (*types.ResponseInitChain, error) {
	app.mu.Lock()
	defer app.mu.Unlock()
	if req.ValidatorSet != nil {
		app.validatorSetUpdates[req.InitialHeight] = *req.ValidatorSet
	}
	return &types.ResponseInitChain{}, nil
}

func (app *Application) PrepareProposal(_ context.Context, req *types.RequestPrepareProposal) (*types.ResponsePrepareProposal, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	app.logger.Debug("prepare proposal", "req", req)

	roundState, txResults, err := app.handleProposal(req.Height, req.Txs)
	if err != nil {
		return &types.ResponsePrepareProposal{}, err
	}
	app.logger.Debug("end of prepare proposal", "app_hash", roundState.AppHash)

	return &types.ResponsePrepareProposal{
		TxRecords:             app.substPrepareTx(req.Txs, req.MaxTxBytes),
		AppHash:               roundState.AppHash,
		TxResults:             txResults,
		ConsensusParamUpdates: nil,
		CoreChainLockUpdate:   nil,
		ValidatorSetUpdate:    app.getValidatorSetUpdate(req.Height),
	}, nil
}

func (app *Application) ProcessProposal(_ context.Context, req *types.RequestProcessProposal) (*types.ResponseProcessProposal, error) {
	app.logger.Debug("process proposal", "req", req)

	roundState, txResults, err := app.handleProposal(req.Height, req.Txs)
	if err != nil {
		return &types.ResponseProcessProposal{
			Status: types.ResponseProcessProposal_REJECT,
		}, err
	}

	return &types.ResponseProcessProposal{
		Status:             types.ResponseProcessProposal_ACCEPT,
		AppHash:            roundState.AppHash,
		TxResults:          txResults,
		ValidatorSetUpdate: app.getValidatorSetUpdate(req.Height),
	}, nil
}

func (app *Application) FinalizeBlock(_ context.Context, req *types.RequestFinalizeBlock) (*types.ResponseFinalizeBlock, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	app.logger.Debug("finalize block", "req", req)

	_, ok := app.roundStates[tmbytes.HexBytes(req.AppHash).String()]
	if !ok {
		return &types.ResponseFinalizeBlock{}, fmt.Errorf("state with apphash %s not found", req.AppHash)
	}

	app.finalizedAppHash = req.AppHash

	return &types.ResponseFinalizeBlock{}, nil
}

func (app *Application) Commit(_ context.Context) (*types.ResponseCommit, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	err := app.newHeight(app.lastCommittedState.Height+1, app.finalizedAppHash)
	if err != nil {
		panic(err)
	}

	resp := &types.ResponseCommit{Data: app.lastCommittedState.AppHash}
	if app.RetainBlocks > 0 && app.lastCommittedState.Height >= app.RetainBlocks {
		resp.RetainHeight = app.lastCommittedState.Height - app.RetainBlocks + 1
	}
	return resp, nil
}

func (app *Application) Info(_ context.Context, req *types.RequestInfo) (*types.ResponseInfo, error) {
	app.mu.Lock()
	defer app.mu.Unlock()
	return &types.ResponseInfo{
		Data:             fmt.Sprintf("{\"size\":%v}", app.lastCommittedState.Size()),
		Version:          version.ABCIVersion,
		AppVersion:       ProtocolVersion,
		LastBlockHeight:  app.lastCommittedState.Height,
		LastBlockAppHash: app.lastCommittedState.AppHash,
	}, nil
}

func (*Application) CheckTx(_ context.Context, req *types.RequestCheckTx) (*types.ResponseCheckTx, error) {
	return &types.ResponseCheckTx{Code: code.CodeTypeOK, GasWanted: 1}, nil
}

// Query returns an associated value or nil if missing.
func (app *Application) Query(_ context.Context, reqQuery *types.RequestQuery) (*types.ResponseQuery, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	switch reqQuery.Path {
	case "/verify-chainlock":
		return &types.ResponseQuery{
			Code: 0,
		}, nil
	}

	if reqQuery.Prove {
		value, err := app.lastCommittedState.db.Get(prefixKey(reqQuery.Data))
		if err != nil {
			panic(err)
		}

		resQuery := types.ResponseQuery{
			Index:  -1,
			Key:    reqQuery.Data,
			Value:  value,
			Height: app.lastCommittedState.Height,
		}

		if value == nil {
			resQuery.Log = "does not exist"
		} else {
			resQuery.Log = "exists"
		}

		return &resQuery, nil
	}

	value, err := app.lastCommittedState.db.Get(prefixKey(reqQuery.Data))
	if err != nil {
		panic(err)
	}

	resQuery := types.ResponseQuery{
		Key:    reqQuery.Data,
		Value:  value,
		Height: app.lastCommittedState.Height,
	}

	if value == nil {
		resQuery.Log = "does not exist"
	} else {
		resQuery.Log = "exists"
	}

	return &resQuery, nil
}

// AddValidatorSetUpdate ...
func (app *Application) AddValidatorSetUpdate(vsu types.ValidatorSetUpdate, height int64) {
	app.mu.Lock()
	defer app.mu.Unlock()
	app.validatorSetUpdates[height] = vsu
}

func (app *Application) Close() error {
	app.mu.Lock()
	defer app.mu.Unlock()

	app.resetRoundStates()
	return app.lastCommittedState.Close()
}

// tx is either "val:pubkey!power" or "key=value" or just arbitrary bytes
func (app *Application) handleTx(roundState *State, tx []byte) *types.ExecTxResult {
	if isPrepareTx(tx) {
		return app.execPrepareTx(tx)
	}

	key, value := parseTx(tx)
	err := roundState.db.Set(prefixKey([]byte(key)), []byte(value))
	if err != nil {
		panic(err)
	}

	events := []types.Event{
		{
			Type: "app",
			Attributes: []types.EventAttribute{
				{Key: "creator", Value: "Cosmoshi Netowoko", Index: true},
				{Key: "key", Value: key, Index: true},
				{Key: "index_key", Value: "index is working", Index: true},
				{Key: "noindex_key", Value: "index is working", Index: false},
			},
		},
	}

	return &types.ExecTxResult{Code: code.CodeTypeOK, Events: events}
}

// appHash returns app hash of current app state.
// As we are using a memdb - just return the big endian size of the db
func (s *State) updateAppHash() {
	appHash := make([]byte, crypto.DefaultAppHashSize)
	binary.PutVarint(appHash, s.Size())

	s.AppHash = appHash
}

func (app *Application) newRound(height int64) (State, error) {
	roundState := State{db: dbm.NewMemDB(), Height: height}
	err := app.lastCommittedState.Copy(&roundState)
	roundState.Height = height
	if err != nil {
		return State{}, fmt.Errorf("cannot copy current state: %w", err)
	}

	return roundState, nil
}

// newHeight frees resources from previous height and starts new height.
// `height` shall be new height, and `committedRound` shall be round from previous commit
// Caller should lock the Application.
func (app *Application) newHeight(height int64, committedRound tmbytes.HexBytes) error {
	if height != app.lastCommittedState.Height+1 {
		return fmt.Errorf("invalid height: expected: %d, got: %d", app.lastCommittedState.Height+1, height)
	}

	// Committed round becomes new state
	// Note it can be empty (eg. on initial height), but State.Copy() should handle it
	roundState, _ := app.roundStates[committedRound.String()]
	err := roundState.Copy(&app.lastCommittedState)
	if err != nil {
		return err
	}

	app.resetRoundStates()
	saveState(app.lastCommittedState)

	return nil
}

func (app *Application) resetRoundStates() {
	for _, state := range app.roundStates {
		state.Close()
	}
	app.roundStates = map[string]State{}
}

func (app *Application) handleProposal(height int64, txs [][]byte) (State, []*types.ExecTxResult, error) {
	roundState, err := app.newRound(height)
	if err != nil {
		return State{}, nil, err
	}

	// execute block
	txResults := make([]*types.ExecTxResult, len(txs))
	for i, tx := range txs {
		txResults[i] = app.handleTx(&roundState, tx)
	}
	roundState.updateAppHash()
	app.roundStates[roundState.AppHash.String()] = roundState

	return roundState, txResults, nil
}

func parseTx(tx []byte) (string, string) {
	parts := bytes.Split(tx, []byte("="))
	if len(parts) == 2 {
		return string(parts[0]), string(parts[1])
	}
	return string(tx), string(tx)
}

//---------------------------------------------

func (app *Application) getValidatorSetUpdate(height int64) *types.ValidatorSetUpdate {
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

// -----------------------------
// prepare proposal machinery

const PreparePrefix = "prepare"

func isPrepareTx(tx []byte) bool {
	return bytes.HasPrefix(tx, []byte(PreparePrefix))
}

// execPrepareTx is noop. tx data is considered as placeholder
// and is substitute at the PrepareProposal.
func (app *Application) execPrepareTx(tx []byte) *types.ExecTxResult {
	// noop
	return &types.ExecTxResult{}
}

// substPrepareTx substitutes all the transactions prefixed with 'prepare' in the
// proposal for transactions with the prefix stripped.
// It marks all of the original transactions as 'REMOVED' so that
// Tendermint will remove them from its mempool.
func (app *Application) substPrepareTx(blockData [][]byte, maxTxBytes int64) []*types.TxRecord {
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
