package consensus

import (
	"context"
	"errors"
	"fmt"

	sync "github.com/sasha-s/go-deadlock"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

type blockExecutor struct {
	mtx                sync.RWMutex
	logger             log.Logger
	privValidator      privValidator
	blockExec          sm.Executor
	proposedAppVersion uint64
	committedState     sm.State
}

// Create the next block to propose and return it. Returns nil block upon error.
//
// We really only need to return the parts, but the block is returned for
// convenience so we can log the proposal block.
//
// NOTE: keep it side-effect free for clarity.
func (c *blockExecutor) create(ctx context.Context, rs *cstypes.RoundState, round int32) (*types.Block, error) {
	if c.privValidator.IsZero() {
		return nil, errors.New("cannot create proposal block on non-validator")
	}

	// TODO(sergio): wouldn't it be easier if CreateProposalBlock accepted cs.LastCommit directly?
	var commit *types.Commit
	switch {
	case rs.Height == c.committedState.InitialHeight:
		// We're creating a proposal for the first block.
		// The commit is empty, but not nil.
		commit = types.NewCommit(0, 0, types.BlockID{}, nil)
	case rs.LastCommit != nil:
		// Make the commit from LastPrecommits
		commit = rs.LastCommit

	default: // This shouldn't happen.
		c.logger.Error("propose step; cannot propose anything without commit for the previous block")
		return nil, nil
	}

	proposerProTxHash := c.privValidator.ProTxHash

	committedState := c.getCommittedState()
	ret, uncommittedState, err := c.blockExec.CreateProposalBlock(ctx, rs.Height, round, committedState, commit, proposerProTxHash, c.proposedAppVersion)
	if err != nil {
		panic(err)
	}
	rs.CurrentRoundState = uncommittedState
	return ret, nil
}

func (c *blockExecutor) ensureProcess(ctx context.Context, rs *cstypes.RoundState, round int32) error {
	block := rs.ProposalBlock
	crs := rs.CurrentRoundState
	if crs.Params.Source != sm.ProcessProposalSource || !crs.MatchesBlock(block.Header, round) {
		c.logger.Trace("CurrentRoundState is outdated, executing ProcessProposal", "crs", crs)
		uncommittedState, err := c.blockExec.ProcessProposal(ctx, block, round, c.committedState, true)
		if err != nil {
			return fmt.Errorf("ProcessProposal abci method: %w", err)
		}
		rs.CurrentRoundState = uncommittedState
	}
	return nil
}

func (c *blockExecutor) mustEnsureProcess(ctx context.Context, rs *cstypes.RoundState, round int32) {
	err := c.ensureProcess(ctx, rs, round)
	if err != nil {
		panic(err)
	}
}

func (c *blockExecutor) finalize(ctx context.Context, stateData *StateData, commit *types.Commit) (sm.State, error) {
	block := stateData.ProposalBlock
	blockParts := stateData.ProposalBlockParts
	return c.blockExec.FinalizeBlock(
		ctx,
		stateData.state.Copy(),
		stateData.CurrentRoundState,
		types.BlockID{
			Hash:          block.Hash(),
			PartSetHeader: blockParts.Header(),
			StateID:       block.StateID().Hash(),
		},
		block,
		commit,
	)
}

func (c *blockExecutor) validate(ctx context.Context, stateData *StateData) error {
	// Validate the block.
	err := c.blockExec.ValidateBlockWithRoundState(ctx, stateData.state, stateData.CurrentRoundState, stateData.ProposalBlock)
	if err != nil {
		step := stateData.Step.String()
		return fmt.Errorf("invalid block %X (step %s): %w", step, stateData.CurrentRoundState.AppHash, err)
	}
	return nil
}

func (c *blockExecutor) mustValidate(ctx context.Context, stateData *StateData) {
	err := c.validate(ctx, stateData)
	if err != nil {
		panic(err)
	}
}

func (c *blockExecutor) setCommittedState(committedState sm.State) {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	c.committedState = committedState
}

func (c *blockExecutor) getCommittedState() sm.State {
	c.mtx.RLock()
	defer c.mtx.RUnlock()
	return c.committedState
}

func (c *blockExecutor) Subscribe(emitter *eventemitter.EventEmitter) {
	emitter.AddListener(setPrivValidatorEventName, func(obj eventemitter.EventData) error {
		c.privValidator = obj.(privValidator)
		return nil
	})
	emitter.AddListener(setProposedAppVersionEventName, func(obj eventemitter.EventData) error {
		c.proposedAppVersion = obj.(uint64)
		return nil
	})
	emitter.AddListener(committedStateUpdateEventName, func(obj eventemitter.EventData) error {
		c.setCommittedState(obj.(sm.State))
		return nil
	})
}
