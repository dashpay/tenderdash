package kvstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strconv"
	"sync"
	"time"

	"github.com/gogo/protobuf/proto"
	dbm "github.com/tendermint/tm-db"

	"github.com/tendermint/tendermint/abci/example/code"
	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/internal/libs/protoio"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	"github.com/tendermint/tendermint/libs/log"
	types1 "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
	"github.com/tendermint/tendermint/version"
)

const ProtocolVersion uint64 = 0x1

//---------------------------------------------------

var _ abci.Application = (*Application)(nil)

// OptFunc is a function that can modify application configuration. Used when creating new application.
type OptFunc func(app *Application) error

// Application is an example implementation of abci.Application.
type Application struct {
	abci.BaseApplication
	mu sync.Mutex

	// LastCommittedState is last state that was committed by Tenderdash and finalized with abci.FinalizeBlock()
	LastCommittedState State
	// roundStates contains state for each round, indexed by AppHash.String()
	roundStates  map[string]State
	RetainBlocks int64 // blocks to retain after commit (via ResponseCommit.RetainHeight)
	logger       log.Logger

	validatorSetUpdates    map[int64]abci.ValidatorSetUpdate
	consensusParamsUpdates map[int64]types1.ConsensusParams

	store StoreFactory

	// Genesis configuration

	cfg Config

	initialHeight         int64 // height of the first minted block
	initialCoreLockHeight uint32

	// Transaction handlers

	// prepareTxs prepares transactions, possibly adding and/or removing some of them
	prepareTxs PrepareTxsFunc
	// verifyTx checks if transaction is correct
	verifyTx VerifyTxFunc
	// execTx executes the transaction against some state
	execTx ExecTxFunc
	// Snapshots

	snapshots       *SnapshotStore
	restoreSnapshot *abci.Snapshot
	restoreAppHash  tmbytes.HexBytes
	restoreChunks   [][]byte
}

// WithValidatorSetUpdates defines initial validator set when creating Application
func WithValidatorSetUpdates(validatorSetUpdates map[int64]abci.ValidatorSetUpdate) OptFunc {
	return func(app *Application) error {
		for height, vsu := range validatorSetUpdates {
			app.AddValidatorSetUpdate(vsu, height)
		}
		return nil
	}
}

// WithLogger sets logger when creating Application
func WithLogger(logger log.Logger) OptFunc {
	return func(app *Application) error {
		app.logger = logger
		return nil
	}
}

// WithState defines last committed state height and apphash of the Application
func WithState(height int64, appHash []byte) OptFunc {
	return func(app *Application) error {
		if len(appHash) == 0 {
			appHash = make([]byte, crypto.DefaultAppHashSize)
		}
		app.LastCommittedState = &kvState{
			DB:      dbm.NewMemDB(),
			AppHash: appHash,
			Height:  height,
		}
		return nil
	}
}

// WithConfig provides Config to new Application
func WithConfig(config Config) OptFunc {
	return func(app *Application) error {
		app.cfg = config
		if config.ValidatorUpdates != nil {
			vsu, err := config.validatorSetUpdates()
			if err != nil {
				return err
			}
			if err := WithValidatorSetUpdates(vsu)(app); err != nil {
				return err
			}
		}
		if config.InitAppInitialCoreHeight != 0 {
			app.initialCoreLockHeight = config.InitAppInitialCoreHeight
		}
		if config.RetainBlocks != 0 {
			app.RetainBlocks = config.RetainBlocks
		}
		return nil
	}
}

// WithExecTx provides custom transaction executing function to the Application
func WithExecTx(execTx ExecTxFunc) OptFunc {
	return func(app *Application) error {
		app.execTx = execTx
		return nil
	}
}

// WithVerifyTxFunc provides custom transaction verification function to the Application
func WithVerifyTxFunc(verifyTx VerifyTxFunc) OptFunc {
	return func(app *Application) error {
		app.verifyTx = verifyTx
		return nil
	}
}

// WithPrepareTxsFunc provides custom transaction modification function to the Application
func WithPrepareTxsFunc(prepareTxs PrepareTxsFunc) OptFunc {
	return func(app *Application) error {
		app.prepareTxs = prepareTxs
		return nil
	}
}

// NewMemoryApp creates new Key/value store application that stores data to memory.
// Data is lost when the app stops.
// The application can be used for testing or as an example of ABCI
// implementation.
// It is possible to alter initial application configs with option functions.
func NewMemoryApp(opts ...OptFunc) (*Application, error) {
	return newApplication(NewMemStateStore(), opts...)
}

func newApplication(stateStore StoreFactory, opts ...OptFunc) (*Application, error) {
	var err error

	app := &Application{
		logger:                 log.NewNopLogger(),
		LastCommittedState:     NewKvState(dbm.NewMemDB(), 0), // initial state to avoid InitChain() in unit tests
		roundStates:            map[string]State{},
		validatorSetUpdates:    map[int64]abci.ValidatorSetUpdate{},
		consensusParamsUpdates: map[int64]types1.ConsensusParams{},
		initialHeight:          1,
		store:                  stateStore,
		prepareTxs:             prepareTxs,
		verifyTx:               verifyTx,
		execTx:                 execTx,
	}

	for _, opt := range opts {
		if err := opt(app); err != nil {
			return nil, err
		}
	}

	// Load state from store if it's available.
	in, err := app.store.Reader()
	if err != nil {
		return nil, fmt.Errorf("open state: %w", err)
	}
	defer in.Close()

	if err := app.LastCommittedState.Load(in); err != nil {
		return nil, fmt.Errorf("load state: %w", err)
	}

	app.snapshots, err = NewSnapshotStore(path.Join(app.cfg.Dir, "snapshots"))
	if err != nil {
		return nil, fmt.Errorf("init snapshot store: %w", err)
	}

	return app, nil
}

// NewPersistentApp creates a new kvstore application that uses json file as persistent storage
// to store state.
func NewPersistentApp(cfg Config, opts ...OptFunc) (*Application, error) {
	options := append([]OptFunc{WithConfig(cfg)}, opts...)
	stateStore := NewFileStore(path.Join(cfg.Dir, "state.json"))
	return newApplication(stateStore, options...)
}

// InitChain implements ABCI
func (app *Application) InitChain(_ context.Context, req *abci.RequestInitChain) (*abci.ResponseInitChain, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	if req.InitialHeight != 0 {
		app.initialHeight = req.InitialHeight
		if app.LastCommittedState.GetHeight() == 0 {
			app.LastCommittedState = NewKvState(dbm.NewMemDB(), app.initialHeight-1)
		}
	}

	if req.InitialCoreHeight != 0 {
		app.initialCoreLockHeight = req.InitialCoreHeight
	}

	// Overwrite state based on AppStateBytes
	if len(req.AppStateBytes) > 0 {
		err := json.Unmarshal(req.AppStateBytes, &app.LastCommittedState)
		if err != nil {
			return &abci.ResponseInitChain{}, err
		}
	}

	if req.ValidatorSet != nil {
		app.validatorSetUpdates[app.initialHeight] = *req.ValidatorSet
	}
	coreChainLock, err := app.chainLockUpdate(req.InitialHeight)
	if err != nil {
		return nil, err
	}
	consensusParams, ok := app.consensusParamsUpdates[app.initialHeight]
	if !ok {
		consensusParams = types1.ConsensusParams{
			Version: &types1.VersionParams{
				AppVersion: ProtocolVersion,
			},
		}
	}
	resp := &abci.ResponseInitChain{
		AppHash:                 app.LastCommittedState.GetAppHash(),
		ConsensusParams:         &consensusParams,
		ValidatorSetUpdate:      app.validatorSetUpdates[app.initialHeight],
		InitialCoreHeight:       app.initialCoreLockHeight,
		NextCoreChainLockUpdate: coreChainLock,
	}

	app.logger.Debug("InitChain", "req", req, "resp", resp)
	return resp, nil
}

// PrepareProposal implements ABCI
func (app *Application) PrepareProposal(_ context.Context, req *abci.RequestPrepareProposal) (*abci.ResponsePrepareProposal, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	if req.MaxTxBytes <= 0 {
		return &abci.ResponsePrepareProposal{}, fmt.Errorf("MaxTxBytes must be positive, got: %d", req.MaxTxBytes)
	}

	txRecords, err := app.prepareTxs(*req)
	if err != nil {
		return &abci.ResponsePrepareProposal{}, err
	}
	includedTxs := txRecords2Txs(txRecords)
	roundState, txResults, err := app.executeProposal(req.Height, includedTxs)
	if err != nil {
		return &abci.ResponsePrepareProposal{}, err
	}
	coreChainLock, err := app.chainLockUpdate(req.Height)
	if err != nil {
		return nil, err
	}
	resp := &abci.ResponsePrepareProposal{
		TxRecords:             txRecords,
		AppHash:               roundState.GetAppHash(),
		TxResults:             txResults,
		ConsensusParamUpdates: app.getConsensusParamsUpdate(req.Height),
		CoreChainLockUpdate:   coreChainLock,
		ValidatorSetUpdate:    app.getValidatorSetUpdate(req.Height),
	}

	if app.cfg.PrepareProposalDelayMS != 0 {
		time.Sleep(time.Duration(app.cfg.PrepareProposalDelayMS) * time.Millisecond)
	}

	app.logger.Debug("PrepareProposal", "app_hash", roundState.GetAppHash(), "req", req, "resp", resp)
	return resp, nil
}

func (app *Application) ProcessProposal(_ context.Context, req *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	roundState, txResults, err := app.executeProposal(req.Height, types.NewTxs(req.Txs))
	if err != nil {
		return &abci.ResponseProcessProposal{
			Status: abci.ResponseProcessProposal_REJECT,
		}, err
	}
	coreChainLock, err := app.chainLockUpdate(req.Height)
	if err != nil {
		return nil, err
	}
	resp := &abci.ResponseProcessProposal{
		Status:                abci.ResponseProcessProposal_ACCEPT,
		AppHash:               roundState.GetAppHash(),
		TxResults:             txResults,
		ConsensusParamUpdates: app.getConsensusParamsUpdate(req.Height),
		CoreChainLockUpdate:   coreChainLock,
		ValidatorSetUpdate:    app.getValidatorSetUpdate(req.Height),
	}

	if app.cfg.ProcessProposalDelayMS != 0 {
		time.Sleep(time.Duration(app.cfg.ProcessProposalDelayMS) * time.Millisecond)
	}

	app.logger.Debug("ProcessProposal", "req", req, "resp", resp)
	return resp, nil
}

// FinalizeBlock implements ABCI
func (app *Application) FinalizeBlock(_ context.Context, req *abci.RequestFinalizeBlock) (*abci.ResponseFinalizeBlock, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	if app.LastCommittedState.GetHeight()+1 != req.Height {
		return &abci.ResponseFinalizeBlock{},
			fmt.Errorf("block at height %d (apphash: %s) already finalized", req.Height, app.LastCommittedState.GetAppHash().String())
	}

	appHash := tmbytes.HexBytes(req.AppHash)
	roundState, ok := app.roundStates[appHash.String()]
	if !ok {
		return &abci.ResponseFinalizeBlock{}, fmt.Errorf("state with apphash %s not found", appHash)
	}
	if roundState.GetHeight() != req.Height {
		return &abci.ResponseFinalizeBlock{},
			fmt.Errorf("height mismatch: expected %d, got %d", roundState.GetHeight(), req.Height)
	}
	events := []abci.Event{app.eventValUpdate(req.Height)}
	resp := &abci.ResponseFinalizeBlock{
		Events: events,
	}
	if app.RetainBlocks > 0 && app.LastCommittedState.GetHeight() >= app.RetainBlocks {
		resp.RetainHeight = app.LastCommittedState.GetHeight() - app.RetainBlocks + 1
	}

	if app.cfg.FinalizeBlockDelayMS != 0 {
		time.Sleep(time.Duration(app.cfg.FinalizeBlockDelayMS) * time.Millisecond)
	}

	err := app.newHeight(appHash)
	if err != nil {
		return &abci.ResponseFinalizeBlock{}, err
	}

	if err := app.createSnapshot(); err != nil {
		return &abci.ResponseFinalizeBlock{}, fmt.Errorf("create snapshot: %w", err)
	}

	if app.RetainBlocks > 0 && app.LastCommittedState.GetHeight() >= app.RetainBlocks {
		resp.RetainHeight = app.LastCommittedState.GetHeight() - app.RetainBlocks + 1
	}

	app.logger.Debug("finalized block", "req", req)

	return resp, nil
}

// eventValUpdate generates an event that contains info about current validator set
func (app *Application) eventValUpdate(height int64) abci.Event {
	vu := app.getValidatorSetUpdate(height)
	event := abci.Event{
		Type: "val_updates",
		Attributes: []abci.EventAttribute{
			{
				Key:   "size",
				Value: strconv.Itoa(len(vu.ValidatorUpdates)),
			},
			{
				Key:   "height",
				Value: strconv.Itoa(int(height)),
			},
		},
	}

	return event
}

// ListSnapshots implements ABCI.
func (app *Application) ListSnapshots(_ context.Context, req *abci.RequestListSnapshots) (*abci.ResponseListSnapshots, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	snapshots, err := app.snapshots.List()
	if err != nil {
		return &abci.ResponseListSnapshots{}, err
	}
	resp := abci.ResponseListSnapshots{Snapshots: snapshots}

	app.logger.Debug("ListSnapshots", "req", req, "resp", resp)
	return &resp, nil
}

// LoadSnapshotChunk implements ABCI.
func (app *Application) LoadSnapshotChunk(_ context.Context, req *abci.RequestLoadSnapshotChunk) (*abci.ResponseLoadSnapshotChunk, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	chunk, err := app.snapshots.LoadChunk(req.Height, req.Format, req.Chunk)
	if err != nil {
		return &abci.ResponseLoadSnapshotChunk{}, err
	}
	resp := &abci.ResponseLoadSnapshotChunk{Chunk: chunk}

	app.logger.Debug("LoadSnapshotChunk", "resp", resp)
	return resp, nil
}

// OfferSnapshot implements ABCI.
func (app *Application) OfferSnapshot(_ context.Context, req *abci.RequestOfferSnapshot) (*abci.ResponseOfferSnapshot, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	if app.restoreSnapshot != nil {
		return &abci.ResponseOfferSnapshot{}, errors.New("a snapshot is already being restored")
	}
	app.restoreSnapshot = req.Snapshot
	app.restoreAppHash = req.AppHash
	app.restoreChunks = [][]byte{}
	resp := &abci.ResponseOfferSnapshot{Result: abci.ResponseOfferSnapshot_ACCEPT}

	app.logger.Debug("OfferSnapshot", "req", req, "resp", resp)
	return resp, nil
}

// ApplySnapshotChunk implements ABCI.
func (app *Application) ApplySnapshotChunk(_ context.Context, req *abci.RequestApplySnapshotChunk) (*abci.ResponseApplySnapshotChunk, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	if app.restoreSnapshot == nil {
		return &abci.ResponseApplySnapshotChunk{}, fmt.Errorf("no restore in progress")
	}

	app.restoreChunks = append(app.restoreChunks, req.Chunk)
	if len(app.restoreChunks) == int(app.restoreSnapshot.Chunks) {
		bz := []byte{}
		for _, chunk := range app.restoreChunks {
			bz = append(bz, chunk...)
		}
		if err := json.Unmarshal(bz, &app.LastCommittedState); err != nil {
			return &abci.ResponseApplySnapshotChunk{}, fmt.Errorf("cannot unmarshal state: %w", err)
		}

		app.logger.Info("restored state snapshot",
			"height", app.LastCommittedState.GetHeight(),
			"json", string(bz),
			"apphash", app.LastCommittedState.GetAppHash(),
			"snapshot_height", app.restoreSnapshot.Height,
			"snapshot_apphash", app.restoreAppHash,
		)
		app.restoreSnapshot = nil
		app.restoreChunks = nil
		app.restoreAppHash = nil
	}

	resp := &abci.ResponseApplySnapshotChunk{Result: abci.ResponseApplySnapshotChunk_ACCEPT}

	app.logger.Debug("ApplySnapshotChunk", "resp", resp)
	return resp, nil
}

func (app *Application) createSnapshot() error {
	height := app.LastCommittedState.GetHeight()
	if app.cfg.SnapshotInterval > 0 && uint64(height)%app.cfg.SnapshotInterval == 0 {
		if _, err := app.snapshots.Create(app.LastCommittedState); err != nil {
			return fmt.Errorf("create snapshot: %w", err)
		}
		app.logger.Info("created state sync snapshot", "height", height, "apphash", app.LastCommittedState.GetAppHash())
	}

	if err := app.snapshots.Prune(maxSnapshotCount); err != nil {
		return fmt.Errorf("prune snapshots: %w", err)
	}

	return nil
}

// Info implements ABCI
func (app *Application) Info(_ context.Context, req *abci.RequestInfo) (*abci.ResponseInfo, error) {
	app.mu.Lock()
	defer app.mu.Unlock()
	appHash := app.LastCommittedState.GetAppHash()
	resp := &abci.ResponseInfo{
		Data:             fmt.Sprintf("{\"appHash\":\"%s\"}", appHash.String()),
		Version:          version.ABCIVersion,
		AppVersion:       ProtocolVersion,
		LastBlockHeight:  app.LastCommittedState.GetHeight(),
		LastBlockAppHash: app.LastCommittedState.GetAppHash(),
	}
	app.logger.Debug("Info", "req", req, "resp", resp)
	return resp, nil
}

// CheckTx implements ABCI
func (app *Application) CheckTx(_ context.Context, req *abci.RequestCheckTx) (*abci.ResponseCheckTx, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	resp, err := app.verifyTx(req.Tx, req.Type)
	if app.cfg.CheckTxDelayMS != 0 {
		time.Sleep(time.Duration(app.cfg.CheckTxDelayMS) * time.Millisecond)
	}

	return &resp, err
}

// Query returns an associated value or nil if missing.
func (app *Application) Query(_ context.Context, reqQuery *abci.RequestQuery) (*abci.ResponseQuery, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	switch reqQuery.Path {
	case "/verify-chainlock":
		return &abci.ResponseQuery{
			Code: 0,
		}, nil
	case "/val":
		vu, err := app.findValidatorUpdate(reqQuery.Data)
		if err != nil {
			return &abci.ResponseQuery{
				Code: code.CodeTypeUnknownError,
				Log:  err.Error(),
			}, nil
		}
		value, err := encodeMsg(&vu)
		if err != nil {
			return &abci.ResponseQuery{
				Code: code.CodeTypeEncodingError,
				Log:  err.Error(),
			}, nil
		}
		return &abci.ResponseQuery{
			Key:   reqQuery.Data,
			Value: value,
		}, nil
	}

	if reqQuery.Prove {
		value, err := app.LastCommittedState.Get(reqQuery.Data)
		if err != nil {
			panic(err)
		}

		resQuery := abci.ResponseQuery{
			Index:  -1,
			Key:    reqQuery.Data,
			Value:  value,
			Height: app.LastCommittedState.GetHeight(),
		}

		if value == nil {
			resQuery.Log = "does not exist"
			resQuery.Code = code.CodeTypeNotFound
		} else {
			resQuery.Log = "exists"
		}

		return &resQuery, nil
	}

	value, err := app.LastCommittedState.Get(reqQuery.Data)
	if err != nil {
		panic(err)
	}

	resQuery := abci.ResponseQuery{
		Key:    reqQuery.Data,
		Value:  value,
		Height: app.LastCommittedState.GetHeight(),
	}

	if value == nil {
		resQuery.Log = "does not exist"
		resQuery.Code = code.CodeTypeNotFound
	} else {
		resQuery.Log = "exists"
	}

	return &resQuery, nil
}

// AddConsensusParamsUpdate schedules new consensus params update to be returned at `height` and used at `height+1`.
// `params` must be a final struct that should be passed to Tenderdash in ResponsePrepare/ProcessProposal at `height`.
func (app *Application) AddConsensusParamsUpdate(params types1.ConsensusParams, height int64) {
	app.mu.Lock()
	defer app.mu.Unlock()
	app.consensusParamsUpdates[height] = params
}

// AddValidatorSetUpdate schedules new valiudator set update at some height
func (app *Application) AddValidatorSetUpdate(vsu abci.ValidatorSetUpdate, height int64) {
	app.mu.Lock()
	defer app.mu.Unlock()
	app.validatorSetUpdates[height] = vsu
}

// Close closes the app gracefully
func (app *Application) Close() error {
	app.mu.Lock()
	defer app.mu.Unlock()

	app.resetRoundStates()
	app.LastCommittedState.Close()

	return nil
}

// newHeight frees resources from previous height and starts new height.
// Caller should lock the Application.
func (app *Application) newHeight(committedAppHash tmbytes.HexBytes) error {
	// Committed round becomes new state
	committedState := app.roundStates[committedAppHash.String()]
	if committedState == nil {
		return fmt.Errorf("round state with apphash %s not found", committedAppHash.String())
	}
	err := committedState.Copy(app.LastCommittedState)
	if err != nil {
		return err
	}

	app.resetRoundStates()
	if err := app.persistInterval(); err != nil {
		return err
	}

	return nil
}

// resetRoundStates closes and cleans up uncommitted round states
func (app *Application) resetRoundStates() {
	for _, state := range app.roundStates {
		state.Close()
	}
	app.roundStates = map[string]State{}
}

// executeProposal executes transactions and creates new candidate state
func (app *Application) executeProposal(height int64, txs types.Txs) (State, []*abci.ExecTxResult, error) {
	if height != app.LastCommittedState.GetHeight()+1 {
		return nil, nil, fmt.Errorf("height mismatch, expected: %d, got: %d", app.LastCommittedState.GetHeight()+1, height)
	}

	// Create new round state based on last committed state, with incremented height

	roundState := NewKvState(dbm.NewMemDB(), 0) // height will be overwritten in Copy()
	if err := app.LastCommittedState.Copy(roundState); err != nil {
		return nil, nil, fmt.Errorf("cannot copy current state: %w", err)
	}
	roundState.IncrementHeight()

	// execute block
	txResults := make([]*abci.ExecTxResult, 0, len(txs))
	for _, tx := range txs {
		result, err := app.execTx(tx, roundState)
		if err != nil && result.Code == 0 {
			result = abci.ExecTxResult{Code: code.CodeTypeUnknownError, Log: err.Error()}
		}
		txResults = append(txResults, &result)
	}

	// Don't update AppHash at genesis height
	if roundState.GetHeight() != app.initialHeight {
		if err := roundState.UpdateAppHash(app.LastCommittedState, txs, txResults); err != nil {
			return nil, nil, fmt.Errorf("update apphash: %w", err)
		}
	}
	app.roundStates[roundState.GetAppHash().String()] = roundState

	return roundState, txResults, nil
}

// getConsensusParamsUpdate returns consensus params update to be returned at given `height`
// and applied at `height+1`
func (app *Application) getConsensusParamsUpdate(height int64) *types1.ConsensusParams {
	if cp, ok := app.consensusParamsUpdates[height]; ok {
		return &cp
	}

	return nil
}

// ---------------------------------------------
// getValidatorSetUpdate returns validator update at some `height` that will be applied at `height+1`.
func (app *Application) getValidatorSetUpdate(height int64) *abci.ValidatorSetUpdate {
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
	return proto.Clone(&vsu).(*abci.ValidatorSetUpdate)
}

func (app *Application) chainLockUpdate(height int64) (*types1.CoreChainLock, error) {
	chainLockUpdateStr, ok := app.cfg.ChainLockUpdates[strconv.FormatInt(height, 10)]
	if !ok {
		return nil, nil
	}
	chainLockUpdate, err := strconv.Atoi(chainLockUpdateStr)
	if err != nil {
		return nil, fmt.Errorf("invalid number chainLockUpdate value %q: %w", chainLockUpdateStr, err)
	}
	chainLock := types.NewMockChainLock(uint32(chainLockUpdate))
	return chainLock.ToProto(), nil
}

// -----------------------------
// validator set updates logic

func (app *Application) getActiveValidatorSetUpdates() abci.ValidatorSetUpdate {
	var closestHeight int64
	for height := range app.validatorSetUpdates {
		if height > closestHeight && height <= app.LastCommittedState.GetHeight() {
			closestHeight = height
		}
	}
	return app.validatorSetUpdates[closestHeight]
}

func (app *Application) findValidatorUpdate(proTxHash crypto.ProTxHash) (abci.ValidatorUpdate, error) {
	vsu := app.getActiveValidatorSetUpdates()
	for _, vu := range vsu.ValidatorUpdates {
		if proTxHash.Equal(vu.ProTxHash) {
			return vu, nil
		}
	}
	return abci.ValidatorUpdate{}, errors.New("validator-update not found")
}

func encodeMsg(data proto.Message) ([]byte, error) {
	buf := bytes.NewBufferString("")
	w := protoio.NewDelimitedWriter(buf)
	_, err := w.WriteMsg(data)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// persist persists application state according to the config
func (app *Application) persist() error {
	out, err := app.store.Writer()
	if err != nil {
		return err
	}
	defer out.Close()
	return app.LastCommittedState.Save(out)
}

// persistInterval persists application state according to persist-interval parameter
func (app *Application) persistInterval() error {
	if app.cfg.PersistInterval == 0 || app.LastCommittedState.GetHeight()%int64(app.cfg.PersistInterval) != 0 {
		return nil
	}
	return app.persist()
}
