package consensus

import (
	"context"
	"fmt"

	"github.com/tendermint/tendermint/dash"
	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/types"
)

type TryAddCommitEvent struct {
	Commit *types.Commit
	PeerID types.NodeID
}

// TryAddCommitCommand ...
// If we received a commit message from an external source try to add it then finalize it.
type TryAddCommitCommand struct {
	logger log.Logger
	// create and execute blocks
	eventPublisher *EventPublisher
	blockExec      *blockExecutor
}

// Execute ...
func (cs *TryAddCommitCommand) Execute(ctx context.Context, behavior *Behavior, stateEvent StateEvent) (any, error) {
	event := stateEvent.Data.(TryAddCommitEvent)
	appState := stateEvent.AppState
	commit := event.Commit
	peerID := event.PeerID

	// Let's only add one remote commit
	if appState.Commit != nil {
		return false, nil
	}

	rs := appState.RoundState

	// We need to first verify that the commit received wasn't for a future round,
	// If it was then we must go to next round
	if commit.Height == rs.Height && commit.Round > rs.Round {
		cs.logger.Debug("Commit received for a later round", "height", commit.Height, "our round",
			rs.Round, "commit round", commit.Round)
		verified, err := cs.verifyCommit(ctx, appState, commit, peerID, true)
		if err != nil {
			return false, err
		}
		if verified {
			_ = behavior.EnterNewRound(ctx, appState, EnterNewRoundEvent{Height: appState.Height, Round: commit.Round})
			//cs.enterNewRoundCommand(ctx, appState, appState.Height, commit.Round)
			// We are now going to receive the block, so initialize the block parts.
			if appState.ProposalBlockParts == nil {
				appState.ProposalBlockParts = types.NewPartSetFromHeader(commit.BlockID.PartSetHeader)
			}

			return false, nil
		}
	}

	// First lets verify that the commit is what we are expecting
	verified, err := cs.verifyCommit(ctx, appState, commit, peerID, false)
	if !verified || err != nil {
		return verified, err
	}

	appState.Commit = commit

	// We need to make sure we are past the Propose step
	if appState.Step <= cstypes.RoundStepPropose {
		// In this case we need to apply the commit after the proposal block comes in
		return false, nil
	}

	// TODO figure out how to return a result of operation
	// for this transition it should be (bool, error)
	return behavior.AddCommit(ctx, appState, AddCommitEvent{Commit: commit})
}

func (cs *TryAddCommitCommand) verifyCommit(ctx context.Context, appState *AppState, commit *types.Commit, peerID types.NodeID, ignoreProposalBlock bool) (verified bool, err error) {
	verified, err = appState.verifyCommit(commit, peerID, ignoreProposalBlock)
	if !verified || err != nil {
		return verified, err
	}
	if ignoreProposalBlock {
		return true, nil
	}
	block, blockParts := appState.ProposalBlock, appState.ProposalBlockParts
	if block == nil {
		return false, nil
	}
	if !blockParts.HasHeader(commit.BlockID.PartSetHeader) {
		return false, fmt.Errorf("expected ProposalBlockParts header to be commit header")
	}
	proTxHash, _ := dash.ProTxHashFromContext(ctx)
	if !block.HashesTo(commit.BlockID.Hash) {
		cs.logger.Error("proposal block does not hash to commit hash",
			"height", commit.Height,
			"node_proTxHash", proTxHash.ShortString(),
			"block", block,
			"commit", commit,
			"complete_proposal", appState.isProposalComplete(),
		)
		return false, fmt.Errorf("cannot finalize commit; proposal block does not hash to commit hash")
	}
	// We have a correct block, let's process it before applying the commit
	err = cs.blockExec.process(ctx, appState, commit.Round)
	if err != nil {
		return false, fmt.Errorf("unable to process proposal: %w", err)
	}
	err = cs.blockExec.validate(ctx, appState)
	if err != nil {
		return false, fmt.Errorf("+2/3 committed an invalid block: %w", err)
	}
	return true, nil
}