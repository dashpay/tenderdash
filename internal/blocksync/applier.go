package blocksync

import (
	"context"
	"fmt"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/tendermint/tendermint/internal/consensus"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/types"
)

type (
	applierOptionFunc func(*blockApplier)
	blockApplier      struct {
		mtx       sync.Mutex
		logger    log.Logger
		blockExec sm.Executor
		store     sm.BlockStore
		state     sm.State
		metrics   *consensus.Metrics
	}
)

func applierWithMetrics(metrics *consensus.Metrics) applierOptionFunc {
	return func(applier *blockApplier) {
		applier.metrics = metrics
	}
}

func applierWithLogger(logger log.Logger) applierOptionFunc {
	return func(applier *blockApplier) {
		applier.logger = logger
	}
}

func applierWithState(state sm.State) applierOptionFunc {
	return func(applier *blockApplier) {
		applier.state = state
	}
}

func newBlockApplier(blockExec sm.Executor, store sm.BlockStore, opts ...applierOptionFunc) *blockApplier {
	applier := &blockApplier{
		blockExec: blockExec,
		store:     store,
		logger:    log.NewNopLogger(),
		metrics:   consensus.NopMetrics(),
	}
	for _, opt := range opts {
		opt(applier)
	}
	return applier
}

// Apply safely verifies, saves to the store and executes a block with commit
func (e *blockApplier) Apply(ctx context.Context, block *types.Block, commit *types.Commit) error {
	e.mtx.Lock()
	defer e.mtx.Unlock()
	blockID := block.BlockID(nil)

	err := e.verify(ctx, blockID, block, commit)
	if err != nil {
		return err
	}

	blockParts, err := block.MakePartSet(types.BlockPartSizeBytes)
	if err != nil {
		return err
	}
	e.store.SaveBlock(block, blockParts, commit)

	// TODO: Same thing for app - but we would need a way to get the hash without persisting the state.
	e.state, err = e.blockExec.ApplyBlock(ctx, e.state, blockID, block, commit)
	if err != nil {
		panic(fmt.Sprintf("failed to process committed block (%d:%X): %v", block.Height, block.Hash(), err))
	}
	e.metrics.RecordConsMetrics(block)
	return nil
}

// State safely returns the last version of a state
func (e *blockApplier) State() sm.State {
	e.mtx.Lock()
	defer e.mtx.Unlock()
	return e.state
}

// UpdateState safely updates a state on a new one
func (e *blockApplier) UpdateState(newState sm.State) {
	e.mtx.Lock()
	defer e.mtx.Unlock()
	e.state = newState
}

func (e *blockApplier) verify(ctx context.Context, blockID types.BlockID, block *types.Block, commit *types.Commit) error {
	err := e.state.Validators.VerifyCommit(e.state.ChainID, blockID, block.Height, commit)

	// If either of the checks failed we log the error and request for a new block
	// at that height
	if err != nil {
		err = fmt.Errorf("invalid a commit: %w", err)
		e.logger.Error(err.Error(),
			"commit", commit,
			"block_id", blockID,
			"height", block.Height,
		)
		return err
	}
	// validate the block before we persist it
	err = e.blockExec.ValidateBlock(ctx, e.state, block)
	if err != nil {
		err = fmt.Errorf("invalid a block: %w", err)
		e.logger.Error(err.Error(),
			"commit", commit,
			"block_id", blockID,
			"height", block.Height,
		)
		return err
	}
	return nil
}
