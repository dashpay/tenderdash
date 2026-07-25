package blocksync

import (
	"context"
	"fmt"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/dashpay/tenderdash/internal/consensus"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
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
		stats     applyStats
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

	// The part set is needed twice: to derive the block ID and to persist the
	// block. Building it serializes the whole block and builds a Merkle tree over
	// the parts, so build it once and pass it to both.
	start := time.Now()
	blockParts, err := block.MakePartSet(types.BlockPartSizeBytes)
	if err != nil {
		return err
	}
	blockID := block.BlockID(blockParts)
	partSetTime := time.Since(start)

	start = time.Now()
	err = e.verify(ctx, blockID, block, commit)
	if err != nil {
		return err
	}
	verifyTime := time.Since(start)

	start = time.Now()
	e.store.SaveBlock(block, blockParts, commit)
	saveTime := time.Since(start)

	start = time.Now()
	// TODO: Same thing for app - but we would need a way to get the hash without persisting the state.
	e.state, err = e.blockExec.ApplyBlock(ctx, e.state, blockID, block, commit)
	if err != nil {
		panic(fmt.Sprintf("failed to process committed block (%d:%X): %v", block.Height, block.Hash(), err))
	}
	execTime := time.Since(start)

	e.stats.add(partSetTime, verifyTime, saveTime, execTime)
	// ByteSize is the size of the serialized block we just built, so the metric
	// costs nothing extra here
	e.metrics.RecordConsMetrics(block, blockParts.ByteSize())
	return nil
}

// Timings returns the average per-block cost of each stage of the apply
// pipeline since the previous call, and resets the counters. It reports false
// when no block was applied since then.
func (e *blockApplier) Timings() (applyTimings, bool) {
	return e.stats.take()
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
		err = fmt.Errorf("invalid block: %w", err)
		e.logger.Error(err.Error(),
			"commit", commit,
			"block_id", blockID,
			"height", block.Height,
		)
		return err
	}
	return nil
}

// applyTimings is the average time a single block spends in each stage of the
// apply pipeline.
type applyTimings struct {
	// PartSet is block serialization plus the Merkle tree over its parts
	PartSet time.Duration
	// Verify is commit signature verification and block validation
	Verify time.Duration
	// Save is persisting the block to the block store
	Save time.Duration
	// Exec is the ABCI round trip and the state store writes that follow it
	Exec time.Duration
}

// applyStats accumulates per-stage timings of the block apply pipeline. Block
// sync applies blocks one at a time, so this is where a slow sync shows up; the
// breakdown attributes it to serialization, signature verification, disk or the
// ABCI application without needing a profiler.
type applyStats struct {
	mtx     sync.Mutex
	blocks  int64
	partSet time.Duration
	verify  time.Duration
	save    time.Duration
	exec    time.Duration
}

// add records the timings of a single applied block
func (s *applyStats) add(partSet, verify, save, exec time.Duration) {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	s.blocks++
	s.partSet += partSet
	s.verify += verify
	s.save += save
	s.exec += exec
}

// take returns the per-block averages accumulated so far and resets the
// counters, so each caller sees the interval since the previous call
func (s *applyStats) take() (applyTimings, bool) {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	if s.blocks == 0 {
		return applyTimings{}, false
	}
	blocks := time.Duration(s.blocks)
	timings := applyTimings{
		PartSet: s.partSet / blocks,
		Verify:  s.verify / blocks,
		Save:    s.save / blocks,
		Exec:    s.exec / blocks,
	}
	s.blocks = 0
	s.partSet, s.verify, s.save, s.exec = 0, 0, 0, 0
	return timings, true
}
