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

type validator interface {
	validate(ctx context.Context, appState *AppState) error
	validateOrPanic(ctx context.Context, appState *AppState)
}

type blockExecutor struct {
	logger             log.Logger
	privValidator      privValidator
	blockExec          *sm.BlockExecutor
	proposedAppVersion uint64
}

// Create the next block to propose and return it. Returns nil block upon error.
//
// We really only need to return the parts, but the block is returned for
// convenience so we can log the proposal block.
//
// NOTE: keep it side-effect free for clarity.
// CONTRACT: cs.privValidator is not nil.
func (c *blockExecutor) create(ctx context.Context, appState *AppState, round int32) (*types.Block, error) {
	if c.privValidator.IsZero() {
		return nil, errors.New("entered createProposalBlock with privValidator being nil")
	}

	// TODO(sergio): wouldn't it be easier if CreateProposalBlock accepted cs.LastCommit directly?
	var commit *types.Commit
	switch {
	case appState.Height == appState.state.InitialHeight:
		// We're creating a proposal for the first block.
		// The commit is empty, but not nil.
		commit = types.NewCommit(0, 0, types.BlockID{}, nil)
	case appState.LastCommit != nil:
		// Make the commit from LastPrecommits
		commit = appState.LastCommit

	default: // This shouldn't happen.
		c.logger.Error("propose step; cannot propose anything without commit for the previous block")
		return nil, nil
	}

	proposerProTxHash := c.privValidator.ProTxHash

	ret, uncommittedState, err := c.blockExec.CreateProposalBlock(ctx, appState.Height, round, appState.state, commit, proposerProTxHash, c.proposedAppVersion)
	if err != nil {
		panic(err)
	}
	appState.RoundState.CurrentRoundState = uncommittedState
	return ret, nil
}

func (c *blockExecutor) process(ctx context.Context, appState *AppState, round int32) error {
	block := appState.ProposalBlock
	if appState.CurrentRoundState.Params.Source != sm.ProcessProposalSource ||
		!appState.CurrentRoundState.MatchesBlock(block.Header, round) {
		c.logger.Debug("CurrentRoundState is outdated", "crs", appState.CurrentRoundState)
		uncommittedState, err := c.blockExec.ProcessProposal(ctx, block, round, appState.state, true)
		if err != nil {
			return fmt.Errorf("ProcessProposal abci method: %w", err)
		}
		appState.CurrentRoundState = uncommittedState
	}
	return nil
}

func (c *blockExecutor) processOrPanic(ctx context.Context, appState *AppState, round int32) {
	err := c.process(ctx, appState, round)
	if err != nil {
		panic(err)
	}
}

func (c *blockExecutor) finalize(ctx context.Context, appState *AppState, commit *types.Commit) (sm.State, error) {
	block := appState.ProposalBlock
	blockParts := appState.ProposalBlockParts
	return c.blockExec.FinalizeBlock(
		ctx,
		appState.state.Copy(),
		appState.CurrentRoundState,
		types.BlockID{
			Hash:          block.Hash(),
			PartSetHeader: blockParts.Header(),
			StateID:       block.StateID().Hash(),
		},
		block,
		commit,
	)
}

func (c *blockExecutor) validate(ctx context.Context, appState *AppState) error {
	// Validate the block.
	err := c.blockExec.ValidateBlockWithRoundState(ctx, appState.state, appState.CurrentRoundState, appState.ProposalBlock)
	if err != nil {
		step := ""
		switch appState.Step {
		case cstypes.RoundStepApplyCommit:
			step = "committed"
		case cstypes.RoundStepPrevote:
			step = "prevoted"
		case cstypes.RoundStepPrecommit:
			step = "precommited"
		}
		return fmt.Errorf("+2/3 %s for an invalid block %X: %w", step, appState.CurrentRoundState.AppHash, err)
	}
	return nil
}

func (c *blockExecutor) validateOrPanic(ctx context.Context, appState *AppState) {
	err := c.validate(ctx, appState)
	if err != nil {
		panic(err)
	}
}
