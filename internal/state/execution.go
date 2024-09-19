//go:generate ../../scripts/mockery_generate.sh Executor

package state

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/eventbus"
	"github.com/dashpay/tenderdash/internal/mempool"
	"github.com/dashpay/tenderdash/libs/log"
	tmstate "github.com/dashpay/tenderdash/proto/tendermint/state"
	"github.com/dashpay/tenderdash/types"
)

//-----------------------------------------------------------------------------
// BlockExecutor handles block execution and state updates.
// It exposes ApplyBlock(), which validates & executes the block, updates state w/ ABCI responses,
// then commits and updates the mempool atomically, then saves state.

type VoteExtender interface {
	ExtendVote(ctx context.Context, vote *types.Vote)
}

type Executor interface {
	VoteExtender
	CreateProposalBlock(
		ctx context.Context,
		height int64,
		round int32,
		state State,
		commit *types.Commit,
		proposerProTxHash []byte,
		proposedAppVersion uint64,
	) (*types.Block, CurrentRoundState, error)

	ProcessProposal(
		ctx context.Context,
		block *types.Block,
		round int32,
		state State,
		verify bool,
	) (CurrentRoundState, error)

	ValidateBlock(ctx context.Context, state State, block *types.Block) error

	ValidateBlockWithRoundState(
		ctx context.Context,
		state State,
		uncommittedState CurrentRoundState,
		block *types.Block,
	) error

	FinalizeBlock(
		ctx context.Context,
		state State,
		uncommittedState CurrentRoundState,
		blockID types.BlockID,
		block *types.Block,
		commit *types.Commit,
	) (State, error)

	ApplyBlock(
		ctx context.Context,
		state State,
		blockID types.BlockID,
		block *types.Block,
		commit *types.Commit,
	) (State, error)

	VerifyVoteExtension(ctx context.Context, vote *types.Vote) error
}

// BlockExecutor provides the context and accessories for properly executing a block.
type BlockExecutor struct {
	// save state, validators, consensus params, abci responses here
	store Store

	// use blockstore for the pruning functions.
	blockStore BlockStore

	// execute the app against this
	appClient abciclient.Client

	// events
	eventPublisher types.BlockEventPublisher

	// manage the mempool lock during commit
	// and update both with block results after commit.
	mempool mempool.Mempool
	evpool  EvidencePool

	logger  log.Logger
	metrics *Metrics

	appHashSize int

	// cache the verification results over a single height
	cache map[string]struct{}

	// detect non-deterministic prepare proposal responses
	lastRequestPrepareProposalHash  []byte
	lastResponsePrepareProposalHash []byte
}

// BlockExecWithLogger is an option function to set a logger to BlockExecutor
func BlockExecWithLogger(logger log.Logger) func(e *BlockExecutor) {
	return func(e *BlockExecutor) {
		e.logger = logger
	}
}

// BockExecWithMetrics is an option function to set a metrics to BlockExecutor
func BockExecWithMetrics(metrics *Metrics) func(e *BlockExecutor) {
	return func(e *BlockExecutor) {
		e.metrics = metrics
	}
}

// BlockExecWithAppClient sets application client to BlockExecutor
func BlockExecWithAppClient(appClient abciclient.Client) func(e *BlockExecutor) {
	return func(e *BlockExecutor) {
		e.appClient = appClient
	}
}

// NewBlockExecutor returns a new BlockExecutor with the passed-in EventBus.
func NewBlockExecutor(
	stateStore Store,
	appClient abciclient.Client,
	pool mempool.Mempool,
	evpool EvidencePool,
	blockStore BlockStore,
	eventBus *eventbus.EventBus,
	opts ...func(e *BlockExecutor),
) *BlockExecutor {
	blockExec := &BlockExecutor{
		eventPublisher: eventBus,
		store:          stateStore,
		appClient:      appClient,
		mempool:        pool,
		evpool:         evpool,
		blockStore:     blockStore,
		cache:          make(map[string]struct{}),
		logger:         log.NewNopLogger(),
		metrics:        NopMetrics(),
		appHashSize:    crypto.DefaultAppHashSize,
	}
	for _, opt := range opts {
		opt(blockExec)
	}
	return blockExec
}

// Copy returns a new instance of BlockExecutor and applies option functions
func (blockExec *BlockExecutor) Copy(opts ...func(e *BlockExecutor)) *BlockExecutor {
	copied := &BlockExecutor{
		eventPublisher: blockExec.eventPublisher,
		store:          blockExec.store,
		appClient:      blockExec.appClient,
		mempool:        blockExec.mempool,
		evpool:         blockExec.evpool,
		blockStore:     blockExec.blockStore,
		cache:          blockExec.cache,
		logger:         blockExec.logger,
		metrics:        blockExec.metrics,
		appHashSize:    blockExec.appHashSize,
	}
	for _, opt := range opts {
		opt(copied)
	}
	return copied
}

func (blockExec *BlockExecutor) Store() Store {
	return blockExec.store
}

// CreateProposalBlock calls state.MakeBlock with evidence from the evpool
// and txs from the mempool. The max bytes must be big enough to fit the commit.
// Up to 1/10th of the block space is allocated for maximum sized evidence.
// The rest is given to txs, up to the max gas.
//
// Contract: application will not return more bytes than are sent over the wire.
func (blockExec *BlockExecutor) CreateProposalBlock(
	ctx context.Context,
	height int64,
	round int32,
	state State,
	commit *types.Commit,
	proposerProTxHash []byte,
	proposedAppVersion uint64,
) (*types.Block, CurrentRoundState, error) {
	maxBytes := state.ConsensusParams.Block.MaxBytes
	maxGas := state.ConsensusParams.Block.MaxGas

	evidence, evSize := blockExec.evpool.PendingEvidence(state.ConsensusParams.Evidence.MaxBytes)

	// Fetch a limited amount of valid txs
	maxDataBytes, err := types.MaxDataBytes(maxBytes, commit, evSize)
	if err != nil {
		return nil, CurrentRoundState{}, fmt.Errorf("create proposal block: %w", err)
	}

	txs := blockExec.mempool.ReapMaxBytesMaxGas(maxDataBytes, maxGas)
	numRequestedTxs := txs.Len()
	block := state.MakeBlock(height, txs, commit, evidence, proposerProTxHash, proposedAppVersion)

	localLastCommit := buildLastCommitInfo(block, state.InitialHeight)
	version := block.Version.ToProto()
	request := abci.RequestPrepareProposal{
		MaxTxBytes:         maxDataBytes,
		Txs:                block.Txs.ToSliceOfBytes(),
		LocalLastCommit:    localLastCommit,
		Misbehavior:        block.Evidence.ToABCI(),
		Height:             block.Height,
		Round:              round,
		Time:               block.Time,
		NextValidatorsHash: block.NextValidatorsHash,

		// Dash's fields
		CoreChainLockedHeight: state.LastCoreChainLockedBlockHeight,
		ProposerProTxHash:     block.ProposerProTxHash,
		ProposedAppVersion:    block.ProposedAppVersion,
		Version:               &version,
		QuorumHash:            state.Validators.QuorumHash,
	}
	start := time.Now()
	response, err := blockExec.appClient.PrepareProposal(ctx, &request)
	if err != nil {
		// The App MUST ensure that only valid (and hence 'processable') transactions
		// enter the mempool. Hence, at this point, we can't have any non-processable
		// transaction causing an error.
		//
		// Also, the App can simply skip any transaction that could cause any kind of trouble.
		// Either way, we cannot recover in a meaningful way, unless we skip proposing
		// this block, repair what caused the error and try again. Hence, we return an
		// error for now (the production code calling this function is expected to panic).
		return nil, CurrentRoundState{}, err
	}

	// ensure that the proposal response is deterministic
	reqHash, respHash := deterministicPrepareProposalHashes(&request, response)
	blockExec.logger.Debug("PrepareProposal executed",
		"request_hash", hex.EncodeToString(reqHash),
		"response_hash", hex.EncodeToString(respHash),
		"height", height,
		"round", round,
		"requested_txs", numRequestedTxs,
		"took", time.Since(start).String(),
	)
	if bytes.Equal(blockExec.lastRequestPrepareProposalHash, reqHash) &&
		!bytes.Equal(blockExec.lastResponsePrepareProposalHash, respHash) {
		// we don't panic here, as we don't want to break this node and
		// remove it from voting quorum
		blockExec.logger.Error("PrepareProposal response is non-deterministic",
			"request_hash", hex.EncodeToString(reqHash),
			"response_hash", hex.EncodeToString(respHash),
		)
	}

	if err := response.Validate(); err != nil {
		return nil, CurrentRoundState{}, fmt.Errorf("PrepareProposal responded with invalid response: %w", err)
	}

	txrSet := types.NewTxRecordSet(response.TxRecords)

	if err := txrSet.Validate(maxDataBytes, block.Txs); err != nil {
		return nil, CurrentRoundState{}, err
	}

	for _, rtx := range txrSet.RemovedTxs() {
		if err := blockExec.mempool.RemoveTxByKey(rtx.Key()); err != nil {
			blockExec.logger.Debug("error removing transaction from the mempool", "error", err, "tx hash", rtx.Hash())
		}
	}
	itxs := txrSet.IncludedTxs()

	if err := validateExecTxResults(response.TxResults, itxs); err != nil {
		return nil, CurrentRoundState{}, fmt.Errorf("invalid tx results: %w", err)
	}

	block.SetTxs(itxs)

	if ver := response.GetAppVersion(); ver > 0 {
		block.Version.App = ver
	}

	rp, err := RoundParamsFromPrepareProposal(response, round)
	if err != nil {
		return nil, CurrentRoundState{}, err
	}
	// update some round state data
	stateChanges, err := state.NewStateChangeset(ctx, rp)
	if err != nil {
		return nil, CurrentRoundState{}, err
	}
	err = stateChanges.UpdateBlock(block)
	if err != nil {
		return nil, CurrentRoundState{}, err
	}

	return block, stateChanges, nil
}

func deterministicPrepareProposalHashes(request *abci.RequestPrepareProposal, response *abci.ResponsePrepareProposal) (requestHash, responseHash []byte) {
	deterministicReq := *request
	deterministicReq.Round = 0

	reqBytes, err := deterministicReq.Marshal()
	if err != nil {
		// should never happen, as we just marshaled it before sending
		panic("failed to marshal RequestPrepareProposal: " + err.Error())
	}
	respBytes, err := response.Marshal()
	if err != nil {
		// should never happen, as we just marshaled it before sending
		panic("failed to marshal ResponsePrepareProposal: " + err.Error())
	}
	return crypto.Checksum(reqBytes), crypto.Checksum(respBytes)
}

// ProcessProposal sends the proposal to ABCI App and verifies the response
func (blockExec *BlockExecutor) ProcessProposal(
	ctx context.Context,
	block *types.Block,
	round int32,
	state State,
	verify bool,
) (CurrentRoundState, error) {
	version := block.Version.ToProto()
	resp, err := blockExec.appClient.ProcessProposal(ctx, &abci.RequestProcessProposal{
		Hash:               block.Header.Hash(),
		Height:             block.Header.Height,
		Round:              round,
		Time:               block.Header.Time,
		Txs:                block.Data.Txs.ToSliceOfBytes(),
		ProposedLastCommit: buildLastCommitInfo(block, state.InitialHeight),
		Misbehavior:        block.Evidence.ToABCI(),
		NextValidatorsHash: block.NextValidatorsHash,

		// Dash's fields
		ProposerProTxHash:     block.ProposerProTxHash,
		CoreChainLockedHeight: block.CoreChainLockedHeight,
		CoreChainLockUpdate:   block.CoreChainLock.ToProto(),
		ProposedAppVersion:    block.ProposedAppVersion,
		Version:               &version,
		QuorumHash:            state.Validators.QuorumHash,
	})
	if err != nil {
		return CurrentRoundState{}, err
	}
	if resp.IsStatusUnknown() {
		return CurrentRoundState{}, fmt.Errorf("ProcessProposal responded with status %s", resp.Status.String())
	}
	if !resp.IsAccepted() {
		return CurrentRoundState{}, ErrBlockRejected
	}
	if err := resp.Validate(); err != nil {
		return CurrentRoundState{}, fmt.Errorf("ProcessProposal responded with invalid response: %w", err)
	}
	if err := validateExecTxResults(resp.TxResults, block.Data.Txs); err != nil {
		return CurrentRoundState{}, fmt.Errorf("invalid tx results: %w", err)
	}

	rp := RoundParamsFromProcessProposal(resp, block.CoreChainLock, round)

	// update some round state data
	stateChanges, err := state.NewStateChangeset(ctx, rp)
	if err != nil {
		return stateChanges, err
	}
	// we force the abci app to return only 32 byte app hashes (set to 20 temporarily)
	if resp.AppHash != nil && len(resp.AppHash) != blockExec.appHashSize {
		blockExec.logger.Error(
			"Client returned invalid app hash size", "bytesLength", len(resp.AppHash),
		)
		return stateChanges, errors.New("invalid App Hash size")
	}

	if verify {
		// Here we check if the ProcessProposal response matches
		// block received from proposer, eg. if `uncommittedState`
		// fields are the same as `block` fields
		err = blockExec.ValidateBlockWithRoundState(ctx, state, stateChanges, block)
		if err != nil {
			return stateChanges, ErrInvalidBlock{err}
		}
	}

	return stateChanges, nil
}

// ValidateBlock validates the given block against the given state.
// If the block is invalid, it returns an error.
// Validation does not mutate state, but does require historical information from the stateDB,
// ie. to verify evidence from a validator at an old height.
func (blockExec *BlockExecutor) ValidateBlock(ctx context.Context, state State, block *types.Block) error {
	hash := block.Hash()
	if _, ok := blockExec.cache[hash.String()]; ok {
		return nil
	}

	err := validateBlock(state, block)
	if err != nil {
		return err
	}

	err = blockExec.evpool.CheckEvidence(ctx, block.Evidence)
	if err != nil {
		return err
	}

	blockExec.cache[hash.String()] = struct{}{}
	return nil
}

func (blockExec *BlockExecutor) ValidateBlockWithRoundState(
	ctx context.Context,
	state State,
	uncommittedState CurrentRoundState,
	block *types.Block,
) error {
	err := blockExec.ValidateBlock(ctx, state, block)
	if err != nil {
		return err
	}

	// Validate app info
	if uncommittedState.AppHash != nil && !bytes.Equal(block.AppHash, uncommittedState.AppHash) {
		return fmt.Errorf(
			"wrong Block.Header.AppHash at state height %d, block %d. Expected %X, got %X",
			uncommittedState.GetHeight(),
			block.Height,
			uncommittedState.AppHash,
			block.AppHash,
		)
	}
	if uncommittedState.ResultsHash != nil && !bytes.Equal(block.ResultsHash, uncommittedState.ResultsHash) {
		return fmt.Errorf("wrong Block.Header.ResultsHash.  Expected %X, got %X",
			uncommittedState.ResultsHash,
			block.ResultsHash,
		)
	}

	if block.Height > state.InitialHeight {
		if err := state.LastValidators.VerifyCommit(
			state.ChainID, state.LastBlockID, block.Height-1, block.LastCommit); err != nil {
			return fmt.Errorf("error validating block: %w", err)
		}
	}
	if !bytes.Equal(block.NextValidatorsHash, uncommittedState.NextValidators.Hash()) {
		return fmt.Errorf("wrong Block.Header.NextValidatorsHash. Expected %X, got %v",
			uncommittedState.NextValidators.Hash(),
			block.NextValidatorsHash,
		)
	}
	if !block.NextConsensusHash.Equal(uncommittedState.NextConsensusParams.HashConsensusParams()) {
		return fmt.Errorf(
			"wrong Block.Header.ConsensusHash. Expected %X, got %X",
			uncommittedState.NextConsensusParams.HashConsensusParams(), block.NextConsensusHash,
		)
	}
	return validateCoreChainLock(block, state)
}

// FinalizeBlock validates the block against the state, fires the relevant events,
// calls FinalizeBlock ABCI endpoint, and saves the new state and responses.
// It returns the new state.
//
// It takes a blockID to avoid recomputing the parts hash.
//
// CONTRACT: The block was already delivered to the ABCI app using either PrepareProposal or ProcessProposal.
// See also ApplyBlock() to deliver proposal and finalize it in one step.

func (blockExec *BlockExecutor) FinalizeBlock(
	ctx context.Context,
	state State,
	uncommittedState CurrentRoundState,
	blockID types.BlockID,
	block *types.Block,
	commit *types.Commit,
) (State, error) {
	// validate the block if we haven't already
	if err := blockExec.ValidateBlockWithRoundState(ctx, state, uncommittedState, block); err != nil {
		return state, ErrInvalidBlock{err}
	}

	// Save ResponseProcessProposal before FinalizeBlock to be able to recover when app state is ahead of tenderdash state
	// (eg. when tenderdash fails just after receiving ResponseFinalizeBlock).
	abciResponses := tmstate.ABCIResponses{
		ProcessProposal: uncommittedState.Params.ToProcessProposal(),
	}
	if err := blockExec.store.SaveABCIResponses(block.Height, abciResponses); err != nil {
		return state, err
	}

	startTime := time.Now().UnixNano()
	fbResp, err := execBlockWithoutState(ctx, blockExec.appClient, block, commit, blockExec.logger)
	if err != nil {
		return state, ErrInvalidBlock{err}
	}
	endTime := time.Now().UnixNano()
	blockExec.metrics.BlockProcessingTime.Observe(float64(endTime-startTime) / 1000000)

	if state, err = state.Update(blockID, &block.Header, &uncommittedState); err != nil {
		return state, fmt.Errorf("commit failed for application: %w", err)
	}

	// Lock mempool, commit app state, update mempoool.
	err = blockExec.flushMempool(ctx, state, block, uncommittedState.TxResults)
	if err != nil {
		return state, fmt.Errorf("commit failed for application: %w", err)
	}

	// Update evpool with the latest state.
	blockExec.evpool.Update(ctx, state, block.Evidence)

	if err = blockExec.store.Save(state); err != nil {
		return state, err
	}

	// Prune old heights, if requested by ABCI app
	blockExec.pruneBlocks(fbResp.RetainHeight)

	// reset the verification cache
	blockExec.cache = make(map[string]struct{})

	// Events are fired after everything else.
	// NOTE: if we crash between Commit and Save, events wont be fired during replay
	es := NewFullEventSet(block, blockID, uncommittedState, state.Validators)
	if err = es.Publish(blockExec.eventPublisher); err != nil {
		blockExec.logger.Error("failed publishing event", "err", err)
	}

	return state, nil
}

// ApplyBlock validates the block against the state, executes it against the app using ProcessProposal ABCI request,
// fires the relevant events, finalizes with FinalizeBlock, and saves the new state and responses.
// It returns the new state.
// It's the only function that needs to be called
// from outside this package to process and commit an entire block.
// It takes a blockID to avoid recomputing the parts hash.
func (blockExec *BlockExecutor) ApplyBlock(
	ctx context.Context,
	state State,
	blockID types.BlockID,
	block *types.Block,
	commit *types.Commit,
) (State, error) {
	uncommittedState, err := blockExec.ProcessProposal(ctx, block, commit.Round, state, true)
	if err != nil {
		return state, err
	}
	return blockExec.FinalizeBlock(ctx, state, uncommittedState, blockID, block, commit)
}

// ExtendVote gets vote-extensions from ABCI and updates vote.VoteExtensions with this value
func (blockExec *BlockExecutor) ExtendVote(ctx context.Context, vote *types.Vote) {
	resp, err := blockExec.appClient.ExtendVote(ctx, &abci.RequestExtendVote{
		Hash:   vote.BlockID.Hash,
		Height: vote.Height,
		Round:  vote.Round,
	})
	if err != nil {
		panic(fmt.Errorf("ExtendVote call failed: %w", err))
	}
	vote.VoteExtensions = types.NewVoteExtensionsFromABCIExtended(resp.VoteExtensions)
}

func (blockExec *BlockExecutor) VerifyVoteExtension(ctx context.Context, vote *types.Vote) error {
	var extensions []*abci.ExtendVoteExtension
	if vote.VoteExtensions != nil {
		extensions = vote.VoteExtensions.ToExtendProto()
	}

	resp, err := blockExec.appClient.VerifyVoteExtension(ctx, &abci.RequestVerifyVoteExtension{
		Hash:               vote.BlockID.Hash,
		Height:             vote.Height,
		Round:              vote.Round,
		ValidatorProTxHash: vote.ValidatorProTxHash,
		VoteExtensions:     extensions,
	})
	if err != nil {
		panic(fmt.Errorf("VerifyVoteExtension call failed: %w", err))
	}

	if !resp.IsOK() {
		return errors.New("invalid vote extension")
	}

	return nil
}

// flushMempool locks the mempool, runs the ABCI flushMempool message, and updates the
// mempool.
// It returns the result of calling abci.flushMempool (the AppHash) and the height to retain (if any).
// The Mempool must be locked during commit and update because state is
// typically reset on flushMempool and old txs must be replayed against committed
// state before new txs are run in the mempool, lest they be invalid.
func (blockExec *BlockExecutor) flushMempool(
	ctx context.Context,
	state State,
	block *types.Block,
	txResults []*abci.ExecTxResult,
) error {
	blockExec.mempool.Lock()
	defer blockExec.mempool.Unlock()

	// while mempool is Locked, flush to ensure all async requests have completed
	// in the ABCI app before flushMempool.
	err := blockExec.mempool.FlushAppConn(ctx)
	if err != nil {
		blockExec.logger.Error("client error during mempool.FlushAppConn", "err", err)
		return err
	}

	blockExec.logger.Info(
		"committed state",
		"height", block.Height,
		"core_height", block.CoreChainLockedHeight,
		"num_txs", len(block.Txs),
		"block_app_hash", fmt.Sprintf("%X", block.AppHash),
	)

	// Update mempool.
	err = blockExec.mempool.Update(
		ctx,
		block.Height,
		block.Txs,
		txResults,
		TxPreCheckForState(state),
		TxPostCheckForState(state),
		state.ConsensusParams.ABCI.RecheckTx,
	)

	return err
}

func buildLastCommitInfo(block *types.Block, initialHeight int64) abci.CommitInfo {
	if block.Height == initialHeight {
		// there is no last commit for the initial height.
		// return an empty value.
		return abci.CommitInfo{}
	}
	return abci.CommitInfo{
		Round:                   block.LastCommit.Round,
		QuorumHash:              block.LastCommit.QuorumHash,
		BlockSignature:          block.LastCommit.ThresholdBlockSignature,
		ThresholdVoteExtensions: block.LastCommit.ThresholdVoteExtensions,
	}
}

// Update returns a copy of state with the fields set using the arguments passed in.
func (state State) Update(
	blockID types.BlockID,
	header *types.Header,
	candidateState *CurrentRoundState,
) (State, error) {

	nextVersion := state.Version

	// NOTE: LastBlockRound, LastStateIDHash, AppHash and VoteExtension has not been populated.
	// It will be filled on state.Save.
	newState := State{
		Version:                          nextVersion,
		ChainID:                          state.ChainID,
		InitialHeight:                    state.InitialHeight,
		LastBlockHeight:                  header.Height,
		LastBlockID:                      blockID,
		LastBlockTime:                    header.Time,
		LastCoreChainLockedBlockHeight:   state.LastCoreChainLockedBlockHeight,
		Validators:                       state.Validators.Copy(),
		LastValidators:                   state.Validators.Copy(),
		LastHeightValidatorsChanged:      state.LastHeightValidatorsChanged,
		ConsensusParams:                  state.ConsensusParams,
		LastHeightConsensusParamsChanged: state.LastHeightConsensusParamsChanged,
		LastResultsHash:                  nil,
		LastAppHash:                      nil,
	}
	err := candidateState.UpdateState(&newState)
	if err != nil {
		return State{}, err
	}
	return newState, nil
}

// SetAppHashSize ...
func (blockExec *BlockExecutor) SetAppHashSize(size int) {
	blockExec.appHashSize = size
}

func execBlock(
	ctx context.Context,
	appConn abciclient.Client,
	block *types.Block,
	commit *types.Commit,
	logger log.Logger,
) (*abci.ResponseFinalizeBlock, error) {
	blockHash := block.Hash()
	evidence := block.Evidence.ToABCI()
	protoBlock, err := block.ToProto()
	if err != nil {
		return nil, err
	}
	blockID := block.BlockID(nil)
	protoBlockID := blockID.ToProto()

	responseFinalizeBlock, err := appConn.FinalizeBlock(
		ctx,
		&abci.RequestFinalizeBlock{
			Hash:        blockHash,
			Height:      block.Height,
			Round:       commit.Round,
			Commit:      commit.ToCommitInfo(),
			Misbehavior: evidence,
			Block:       protoBlock,
			BlockID:     &protoBlockID,
		},
	)
	if err != nil {
		logger.Error("executing block", "err", err)
		return responseFinalizeBlock, err
	}
	logger.Debug("executed block", "height", block.Height)

	return responseFinalizeBlock, nil
}

// ----------------------------------------------------------------------------------------------------
// Execute block without state. TODO: eliminate
// ExecReplayedCommitBlock executes and commits a block on the proxyApp without validating or mutating the state.
// It returns the application root hash (apphash - result of abci.flushMempool).
//
// CONTRACT: Block should already be delivered to the app with PrepareProposal or ProcessProposal
func ExecReplayedCommitBlock(
	ctx context.Context,
	appConn abciclient.Client,
	block *types.Block,
	commit *types.Commit,
	logger log.Logger,
) (*abci.ResponseFinalizeBlock, error) {
	fbResp, err := execBlockWithoutState(ctx, appConn, block, commit, logger)
	if err != nil {
		return nil, err
	}

	return fbResp, nil
}

func execBlockWithoutState(
	ctx context.Context,
	appConn abciclient.Client,
	block *types.Block,
	commit *types.Commit,
	logger log.Logger,
) (*abci.ResponseFinalizeBlock, error) {
	respFinalizeBlock, err := execBlock(ctx, appConn, block, commit, logger)
	if err != nil {
		logger.Error("executing block", "err", err)
		return respFinalizeBlock, err
	}

	return respFinalizeBlock, nil
}

func (blockExec *BlockExecutor) pruneBlocks(retainHeight int64) {
	if retainHeight <= 0 {
		return
	}
	base := blockExec.blockStore.Base()
	if retainHeight <= base {
		return
	}
	pruned, err := blockExec.blockStore.PruneBlocks(retainHeight)
	if err != nil {
		blockExec.logger.Error("failed to prune blocks", "retain_height", retainHeight, "err", err)
		return
	}
	err = blockExec.Store().PruneStates(retainHeight)
	if err != nil {
		blockExec.logger.Error("failed to prune state store", "retain_height", retainHeight, "err", err)
		return
	}
	blockExec.logger.Debug("pruned blocks", "pruned", pruned, "retain_height", retainHeight)
}

func validatePubKey(pk crypto.PubKey) error {
	v, ok := pk.(crypto.Validator)
	if !ok {
		return nil
	}
	if err := v.Validate(); err != nil {
		return err
	}
	return nil
}

// validateExecTxResults ensures that tx results are correct.
func validateExecTxResults(txResults []*abci.ExecTxResult, acceptedTxs []types.Tx) error {
	if len(txResults) != len(acceptedTxs) {
		return fmt.Errorf("got %d tx results when there are %d accepted transactions", len(txResults), len(acceptedTxs))
	}
	return nil
}
