package consensus

import (
	"context"
	"errors"
	"fmt"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/types"
)

type blockExecutor struct {
	logger             log.Logger
	privValidator      privValidator
	blockExec          sm.Executor
	proposedAppVersion uint64
}

// Create the next block to propose and return it. Returns nil block upon error.
//
// We really only need to return the parts, but the block is returned for
// convenience so we can log the proposal block.
//
// NOTE: keep it side-effect free for clarity.
// CONTRACT: cs.privValidator is not nil.
func (c *blockExecutor) create(ctx context.Context, stateData *StateData, round int32) (*types.Block, error) {
	if c.privValidator.IsZero() {
		return nil, errors.New("entered createProposalBlock with privValidator being nil")
	}

	// TODO(sergio): wouldn't it be easier if CreateProposalBlock accepted cs.LastCommit directly?
	var commit *types.Commit
	switch {
	case stateData.Height == stateData.state.InitialHeight:
		// We're creating a proposal for the first block.
		// The commit is empty, but not nil.
		commit = types.NewCommit(0, 0, types.BlockID{}, nil)
	case stateData.LastCommit != nil:
		// Make the commit from LastPrecommits
		commit = stateData.LastCommit

	default: // This shouldn't happen.
		c.logger.Error("propose step; cannot propose anything without commit for the previous block")
		return nil, nil
	}

	proposerProTxHash := c.privValidator.ProTxHash

	ret, uncommittedState, err := c.blockExec.CreateProposalBlock(ctx, stateData.Height, round, stateData.state, commit, proposerProTxHash, c.proposedAppVersion)
	if err != nil {
		panic(err)
	}
	stateData.RoundState.CurrentRoundState = uncommittedState
	return ret, nil
}

func (c *blockExecutor) process(ctx context.Context, stateData *StateData, round int32) error {
	block := stateData.ProposalBlock
	crs := stateData.CurrentRoundState
	if crs.Params.Source != sm.ProcessProposalSource || !crs.MatchesBlock(block.Header, round) {
		c.logger.Debug("CurrentRoundState is outdated", "crs", crs)
		uncommittedState, err := c.blockExec.ProcessProposal(ctx, block, round, stateData.state, true)
		if err != nil {
			return fmt.Errorf("ProcessProposal abci method: %w", err)
		}
		stateData.CurrentRoundState = uncommittedState
	}
	return nil
}

func (c *blockExecutor) mustProcess(ctx context.Context, stateData *StateData, round int32) {
	err := c.process(ctx, stateData, round)
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
		step := ""
		switch stateData.Step {
		case cstypes.RoundStepApplyCommit:
			step = "committed"
		case cstypes.RoundStepPrevote:
			step = "prevoted"
		case cstypes.RoundStepPrecommit:
			step = "precommited"
		}
		return fmt.Errorf("+2/3 %s for an invalid block %X: %w", step, stateData.CurrentRoundState.AppHash, err)
	}
	return nil
}

func (c *blockExecutor) validateOrPanic(ctx context.Context, stateData *StateData) {
	err := c.validate(ctx, stateData)
	if err != nil {
		panic(err)
	}
}
