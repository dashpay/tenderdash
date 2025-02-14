package consensus

import (
	"context"
	"fmt"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/merkle"
	"github.com/dashpay/tenderdash/internal/eventbus"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// NewReplayBlockExecutor returns a new instance of state.BlockExecutor configured for BlockReplayer
func NewReplayBlockExecutor(
	appClient abciclient.Client,
	stateStore sm.Store,
	blockStore sm.BlockStore,
	eventBus *eventbus.EventBus,
	opts ...func(e *sm.BlockExecutor),
) *sm.BlockExecutor {
	blockExec := sm.NewBlockExecutor(
		stateStore,
		appClient,
		emptyMempool{},
		sm.EmptyEvidencePool{},
		blockStore,
		eventBus,
		opts...,
	)
	return blockExec
}

// BlockReplayer replays persisted blocks for ABCI application
type BlockReplayer struct {
	stateStore    sm.Store
	store         sm.BlockStore
	genDoc        *types.GenesisDoc
	logger        log.Logger
	nodeProTxHash crypto.ProTxHash
	appClient     abciclient.Client
	blockExec     *sm.BlockExecutor
	publisher     types.BlockEventPublisher
	nBlocks       int // number of blocks applied to the state
}

// ReplayerWithLogger sets logger to BlockReplayer
func ReplayerWithLogger(logger log.Logger) func(r *BlockReplayer) {
	return func(r *BlockReplayer) {
		r.logger = logger
	}
}

// ReplayerWithProTxHash sets node's pro-tx hash to BlockReplayer
func ReplayerWithProTxHash(proTxHash types.ProTxHash) func(r *BlockReplayer) {
	return func(r *BlockReplayer) {
		r.nodeProTxHash = proTxHash
	}
}

// NewBlockReplayer creates and returns an instance of BlockReplayer
func NewBlockReplayer(
	appClient abciclient.Client,
	stateStore sm.Store,
	store sm.BlockStore,
	genDoc *types.GenesisDoc,
	publisher types.BlockEventPublisher,
	blockExec *sm.BlockExecutor,
	opts ...func(r *BlockReplayer),
) *BlockReplayer {
	replayer := &BlockReplayer{
		appClient:  appClient,
		stateStore: stateStore,
		store:      store,
		genDoc:     genDoc,
		nBlocks:    0,
		logger:     log.NewNopLogger(),
		blockExec:  blockExec,
		publisher:  publisher,
	}
	for _, opt := range opts {
		opt(replayer)
	}
	return replayer
}

// Replay syncs persisted blocks with ABCI application
func (r *BlockReplayer) Replay(
	ctx context.Context,
	state sm.State,
	appHash []byte,
	appHeight int64,
) ([]byte, error) {
	rs := replayState{
		storeHeight: r.store.Height(),
		storeBase:   r.store.Base(),
		stateHeight: state.LastBlockHeight,
		appHeight:   appHeight,
		appHash:     appHash,
	}
	r.logger.Info("ABCI Replay Blocks",
		"appHeight", appHeight,
		"storeHeight", rs.storeHeight,
		"stateHeight", rs.stateHeight,
		"storeCoreChainLockedHeight", r.store.CoreChainLockedHeight(),
		"stateCoreChainLockedHeight", state.LastCoreChainLockedBlockHeight,
	)
	err := r.execInitChain(ctx, &rs, &state)
	if err != nil {
		return nil, err
	}
	err = rs.validate(state)
	if err != nil {
		return rs.appHash, err
	}
	if rs.storeHeight == 0 {
		return rs.appHash, nil
	}
	// Now either store is equal to state, or one ahead.
	// For each, consider all cases of where the app could be, given app <= store
	funcs := []func(context.Context, replayState, sm.State) ([]byte, error){
		r.syncStateIfItIsEqualStore,
		r.syncStateIfItIsOneAheadOfStore,
	}
	for _, fn := range funcs {
		appHash, err = fn(ctx, rs, state)
		if appHash != nil || err != nil {
			return appHash, err
		}
	}
	return nil, fmt.Errorf("uncovered case! appHeight: %d, storeHeight: %d, stateHeight: %d",
		rs.appHeight, rs.storeHeight, rs.stateHeight)
}

func (r *BlockReplayer) syncStateIfItIsEqualStore(ctx context.Context, rs replayState, state sm.State) ([]byte, error) {
	if rs.storeHeight != rs.stateHeight {
		return nil, nil
	}
	// Tendermint ran Commit and saved the state.
	// Either the app is asking for replay, or we're all synced up.
	if rs.appHeight < rs.storeHeight {
		// the app is behind, so blockReplayer blocks, but no need to go through WAL (state is already synced to store)
		return r.replayBlocks(ctx, rs, state, false)
	}
	if rs.appHeight == rs.storeHeight {
		// We're good!
		err := checkAppHashEqualsOneFromState(rs.appHash, state)
		if err != nil {
			return nil, err
		}
		return rs.appHash, nil
	}
	return nil, nil
}

func (r *BlockReplayer) syncStateIfItIsOneAheadOfStore(ctx context.Context, rs replayState, state sm.State) ([]byte, error) {
	if rs.storeHeight != rs.stateHeight+1 {
		return nil, nil
	}
	var err error
	// We saved the block in the store but haven't updated the state,
	// so we'll need to replay a block using the WAL.
	if rs.appHeight < rs.stateHeight {
		// the app is further behind than it should be, so replay blocks
		// but leave the last block to go through the WAL
		return r.replayBlocks(ctx, rs, state, true)
	}
	if rs.appHeight == rs.stateHeight {
		// Store is ahead of both App and State
		// We haven't run FinalizeBlock (both the state and app are one block behind),
		// so replay with the real app.
		// NOTE: We could instead use the cs.WAL on cs.Start,
		// but we'd have to allow the WAL to block a block that wrote its #ENDHEIGHT
		r.logger.Info("Replay last block using real app")
		state, err = r.syncStateAt(ctx, state, rs.storeHeight, r.blockExec)
		if err != nil {
			return nil, err
		}
		return state.LastAppHash, nil
	}
	if rs.appHeight == rs.storeHeight {
		// Store and App are ahead of State
		// We ran Commit, but didn't save the state, so replay with mock app.
		abciResponses, err := r.stateStore.LoadABCIResponses(rs.storeHeight)
		if err != nil {
			return nil, err
		}
		mockApp, err := newMockProxyApp(r.logger, rs.appHash, abciResponses)
		if err != nil {
			return nil, err
		}
		if err := mockApp.Start(ctx); err != nil {
			return nil, err
		}
		r.logger.Info("Replay last block using mock app")
		blockExec := r.blockExec.Copy(sm.BlockExecWithAppClient(mockApp))
		state, err = r.syncStateAt(ctx, state, rs.storeHeight, blockExec)
		if err != nil {
			return nil, err
		}
		return state.LastAppHash, nil
	}
	return nil, nil
}

func (r *BlockReplayer) replayBlocks(
	ctx context.Context,
	rs replayState,
	state sm.State,
	mutateState bool,
) ([]byte, error) {
	// App is further behind than it should be, so we need to replay blocks.
	// We replay all blocks from appBlockHeight+1.
	//
	// Note that we don't have an old version of the state,
	// so we by-pass state validation/mutation using sm.ExecCommitBlock.
	// This also means we won't be saving validator sets if they change during this period.
	// TODO: Load the historical information to fix this and just use state.ApplyBlock
	//
	// If mutateState == true, the final block is replayed with r.syncStateAt()
	var err error
	finalBlock := rs.storeHeight
	if mutateState {
		finalBlock--
	}
	firstBlock := rs.appHeight + 1
	if firstBlock == 1 {
		firstBlock = state.InitialHeight
	}
	var (
		block   *types.Block
		commit  *types.Commit
		ucState sm.CurrentRoundState
	)
	for i := firstBlock; i <= finalBlock; i++ {
		block = r.store.LoadBlock(i)
		commit = r.store.LoadSeenCommitAt(i)
		ucState, err = r.replayBlock(ctx, block, commit, state, i)
		if err != nil {
			return nil, err
		}
	}
	if !mutateState {
		err = r.publishEvents(block, ucState)
		if err != nil {
			return nil, err
		}
	}
	appHash := ucState.AppHash
	if mutateState {
		// sync the final block
		state, err = r.syncStateAt(ctx, state, rs.storeHeight, r.blockExec)
		if err != nil {
			return nil, err
		}
		appHash = state.LastAppHash
	}
	if err := checkAppHashEqualsOneFromState(appHash, state); err != nil {
		return nil, err
	}
	return appHash, nil
}

// replayBlock adds block at height H to the application
func (r *BlockReplayer) replayBlock(
	ctx context.Context,
	block *types.Block,
	commit *types.Commit,
	state sm.State,
	height int64,
) (sm.CurrentRoundState, error) {
	r.logger.Info("Replay: applying block", "height", height)
	// Extra check to ensure the app was not changed in a way it shouldn't have.
	ucState, err := r.blockExec.ProcessProposal(ctx, block, commit.Round, state, false)
	if err != nil {
		return sm.CurrentRoundState{}, fmt.Errorf("blockReplayer process proposal: %w", err)
	}
	// We emit events for the index services at the final block due to the sync issue when
	// the node shutdown during the block committing status.
	// For all other cases, we disable emitting events by providing blockExec=nil in ExecReplayedCommitBlock
	_, err = sm.ExecReplayedCommitBlock(ctx, r.appClient, block, commit, r.logger)
	if err != nil {
		return sm.CurrentRoundState{}, err
	}
	// Extra check to ensure the app was not changed in a way it shouldn't have.
	if err := checkAppHashEqualsOneFromBlock(ucState.AppHash, block); err != nil {
		return sm.CurrentRoundState{}, err
	}
	r.nBlocks++
	return ucState, nil
}

// syncStateAt loads block's data for a height H to sync it with the application.
// In order to sync block is used BlockExecutor.ApplyBlock method
func (r *BlockReplayer) syncStateAt(
	ctx context.Context,
	state sm.State,
	height int64,
	blockExec *sm.BlockExecutor,
) (sm.State, error) {
	block := r.store.LoadBlock(height)
	meta := r.store.LoadBlockMeta(height)
	seenCommit := r.store.LoadSeenCommitAt(height)
	// Use stubs for both mempool and evidence pool since no transactions nor
	// evidence are needed here - block already exists.
	state, err := blockExec.ApplyBlock(ctx, state, meta.BlockID, block, seenCommit)
	if err != nil {
		return sm.State{}, err
	}
	r.nBlocks++
	return state, nil
}

func (r *BlockReplayer) execInitChain(ctx context.Context, rs *replayState, state *sm.State) error {
	if rs.appHeight != 0 {
		return nil
	}
	stateBlockHeight := state.LastBlockHeight
	nextVals, err := validatorSetUpdateFromGenesis(r.genDoc)
	if err != nil {
		return err
	}
	pbParams := r.genDoc.ConsensusParams.ToProto()
	res, err := r.appClient.InitChain(ctx, newInitChainRequest(r.genDoc, &pbParams, nextVals))
	if err != nil {
		return fmt.Errorf("execInitChain error from abci: %v", err)
	}
	r.logger.Debug("Response from Init Chain", "res", res.String())
	rs.appHash = res.AppHash

	if stateBlockHeight != 0 {
		return nil
	}

	quorumType := state.Validators.QuorumType
	if err := quorumType.Validate(); err != nil {
		r.logger.Debug("state quorum type validation failed, falling back to genesis one", "err", err)
		quorumType = r.genDoc.QuorumType
	}

	var valParams *types.ValidatorParams
	if res.ConsensusParams != nil && res.ConsensusParams.Validator != nil {
		params := types.ValidatorParamsFromProto(res.ConsensusParams.Validator)
		valParams = &params
	}

	if len(res.ValidatorSetUpdate.ValidatorUpdates) != 0 {
		// we replace existing validator with the one from InitChain instead of applying it as a diff
		state.Validators = types.NewValidatorSet(nil, nil, quorumType, nil, false, valParams)
		// } else if valParams != nil && valParams.VotingPowerThreshold != nil {
		// 	// we update the existing validator set with the new voting threshold
		// 	state.Validators.VotingPowerThreshold = *valParams.VotingPowerThreshold
	}

	// we only update state when we are in initial state
	// If the app did not return an app hash, we keep the one set from the genesis doc in
	// the state. We don't set appHash since we don't want the genesis doc app hash
	// recorded in the genesis block. We should probably just remove GenesisDoc.AppHash.
	rp, err := sm.RoundParamsFromInitChain(res)
	if err != nil {
		return err
	}

	// Allow overriding genesis block time
	if res.GetGenesisTime() != nil {
		state.LastBlockTime = *res.GetGenesisTime()
	}

	candidateState, err := state.NewStateChangeset(ctx, rp)
	if err != nil {
		return err
	}
	err = candidateState.UpdateState(state)
	if err != nil {
		return err
	}
	state.LastCoreChainLockedBlockHeight = res.InitialCoreHeight
	// We update the last results hash with the empty hash, to conform with RFC-6962.
	state.LastResultsHash = merkle.HashFromByteSlices(nil)

	return r.stateStore.Save(*state)
}

func (r *BlockReplayer) publishEvents(
	block *types.Block,
	ucState sm.CurrentRoundState,
) error {
	blockID := block.BlockID(nil)
	es := sm.NewFullEventSet(block, blockID, ucState, ucState.NextValidators)
	err := es.Publish(r.publisher)
	if err != nil {
		r.logger.Error("failed publishing event", "err", err)
	}
	return nil
}

func validatorSetUpdateFromGenesis(genDoc *types.GenesisDoc) (*abci.ValidatorSetUpdate, error) {
	if len(genDoc.QuorumHash) != crypto.DefaultHashSize {
		return nil, nil
	}
	validators := make([]*types.Validator, len(genDoc.Validators))
	for i, val := range genDoc.Validators {
		validators[i] = types.NewValidatorDefaultVotingPower(val.PubKey, val.ProTxHash)
		err := validators[i].ValidateBasic()
		if err != nil {
			return nil, fmt.Errorf("blockReplayer blocks error when validating validator: %s", err)
		}
	}

	var validatorParams types.ValidatorParams
	if genDoc.ConsensusParams != nil {
		validatorParams = genDoc.ConsensusParams.Validator
	}

	validatorSet := types.NewValidatorSetCheckPublicKeys(
		validators,
		genDoc.ThresholdPublicKey,
		genDoc.QuorumType,
		genDoc.QuorumHash,
		&validatorParams,
	)
	err := validatorSet.ValidateBasic()
	if err != nil {
		return nil, fmt.Errorf("blockReplayer blocks error when validating validatorSet: %s", err)
	}
	vals := types.TM2PB.ValidatorUpdates(validatorSet)
	return &vals, err
}

func newInitChainRequest(
	genDoc *types.GenesisDoc,
	csParams *tmproto.ConsensusParams,
	validatorSetUpdate *abci.ValidatorSetUpdate,
) *abci.RequestInitChain {
	return &abci.RequestInitChain{
		Time:              genDoc.GenesisTime,
		ChainId:           genDoc.ChainID,
		InitialHeight:     genDoc.InitialHeight,
		ConsensusParams:   csParams,
		ValidatorSet:      validatorSetUpdate,
		AppStateBytes:     genDoc.AppState,
		InitialCoreHeight: genDoc.InitialCoreChainLockedHeight,
	}
}

type replayState struct {
	storeHeight int64
	storeBase   int64
	stateHeight int64
	appHeight   int64
	appHash     []byte
}

func (rs *replayState) validate(state sm.State) error {
	switch {
	case rs.storeHeight == 0:
		if err := checkAppHashEqualsOneFromState(rs.appHash, state); err != nil {
			return err
		}
	case rs.appHeight == 0 && state.InitialHeight < rs.storeBase:
		// the app has no state, and the block store is truncated above the initial height
		return sm.ErrAppBlockHeightTooLow{
			AppHeight: rs.appHeight,
			StoreBase: rs.storeBase,
		}

	case rs.appHeight > 0 && rs.appHeight < rs.storeBase-1:
		// the app is too far behind truncated store (can be 1 behind since we replay the next)
		return sm.ErrAppBlockHeightTooLow{
			AppHeight: rs.appHeight,
			StoreBase: rs.storeBase,
		}

	case rs.storeHeight < rs.appHeight:
		// the app should never be ahead of the store (but this is under app's control)
		return sm.ErrAppBlockHeightTooHigh{
			CoreHeight: rs.storeHeight,
			AppHeight:  rs.appHeight,
		}

	case rs.storeHeight < rs.stateHeight:
		// the state should never be ahead of the store (this is under tendermint's control)
		return fmt.Errorf("StateBlockHeight (%d) > StoreBlockHeight (%d)", rs.stateHeight, rs.storeHeight)

	case rs.storeHeight > rs.stateHeight+1:
		// store should be at most one ahead of the state (this is under tendermint's control)
		return fmt.Errorf("StoreBlockHeight (%d) > StateBlockHeight + 1 (%d)", rs.storeHeight, rs.stateHeight+1)
	}
	return nil
}
