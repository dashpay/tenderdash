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

// GetType returns TryAddCommitType event-type
func (e *TryAddCommitEvent) GetType() EventType {
	return TryAddCommitType
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
func (cs *TryAddCommitCommand) Execute(ctx context.Context, stateEvent StateEvent) error {
	event := stateEvent.Data.(*TryAddCommitEvent)
	stateData := stateEvent.StateData
	commit := event.Commit
	peerID := event.PeerID

	// Let's only add one remote commit
	if stateData.Commit != nil {
		return nil
	}

	rs := stateData.RoundState

	// We need to first verify that the commit received wasn't for a future round,
	// If it was then we must go to next round
	if commit.Height == rs.Height && commit.Round > rs.Round {
		cs.logger.Debug("Commit received for a later round", "height", commit.Height, "our round",
			rs.Round, "commit round", commit.Round)
		verified, err := cs.verifyCommit(ctx, stateData, commit, peerID, true)
		if err != nil {
			return err
		}
		if verified {
			_ = stateEvent.FSM.Dispatch(ctx, &EnterNewRoundEvent{Height: stateData.Height, Round: commit.Round}, stateData)
			// We are now going to receive the block, so initialize the block parts.
			if stateData.ProposalBlockParts == nil {
				stateData.ProposalBlockParts = types.NewPartSetFromHeader(commit.BlockID.PartSetHeader)
			}

			return nil
		}
	}

	// First lets verify that the commit is what we are expecting
	verified, err := cs.verifyCommit(ctx, stateData, commit, peerID, false)
	if !verified || err != nil {
		return err
	}

	stateData.Commit = commit

	// We need to make sure we are past the Propose step
	if stateData.Step <= cstypes.RoundStepPropose {
		// In this case we need to apply the commit after the proposal block comes in
		return nil
	}
	return stateEvent.FSM.Dispatch(ctx, &AddCommitEvent{Commit: commit}, stateData)
}

func (cs *TryAddCommitCommand) verifyCommit(ctx context.Context, stateData *StateData, commit *types.Commit, peerID types.NodeID, ignoreProposalBlock bool) (verified bool, err error) {
	verified, err = stateData.verifyCommit(commit, peerID, ignoreProposalBlock)
	if !verified || err != nil {
		return verified, err
	}
	if ignoreProposalBlock {
		return true, nil
	}
	block, blockParts := stateData.ProposalBlock, stateData.ProposalBlockParts
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
			"complete_proposal", stateData.isProposalComplete(),
		)
		return false, fmt.Errorf("cannot finalize commit; proposal block does not hash to commit hash")
	}
	// We have a correct block, let's process it before applying the commit
	err = cs.blockExec.process(ctx, stateData, commit.Round)
	if err != nil {
		return false, fmt.Errorf("unable to process proposal: %w", err)
	}
	err = cs.blockExec.validate(ctx, stateData)
	if err != nil {
		return false, fmt.Errorf("+2/3 committed an invalid block: %w", err)
	}
	return true, nil
}
