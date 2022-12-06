package consensus

import (
	"context"
	"errors"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/types"
)

// ProposalBlockCreator ...
type ProposalBlockCreator struct {
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
func (c *ProposalBlockCreator) Create(ctx context.Context, appState *AppState, round int32) (*types.Block, error) {
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
