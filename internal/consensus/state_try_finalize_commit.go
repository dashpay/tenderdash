package consensus

import (
	"context"
	"fmt"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	tmstrings "github.com/dashpay/tenderdash/internal/libs/strings"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/log"
)

type TryFinalizeCommitEvent struct {
	Height int64
}

// GetType returns TryFinalizeCommitType event-type
func (e *TryFinalizeCommitEvent) GetType() EventType {
	return TryFinalizeCommitType
}

// TryFinalizeCommitAction ...
// If we have the block AND +2/3 commits for it, finalize.
type TryFinalizeCommitAction struct {
	logger log.Logger
	// create and execute blocks
	blockExec  *blockExecutor
	blockStore sm.BlockStore
}

// Execute ...
func (cs *TryFinalizeCommitAction) Execute(ctx context.Context, stateEvent StateEvent) error {
	event := stateEvent.Data.(*TryFinalizeCommitEvent)
	stateData := stateEvent.StateData
	if stateData.Height != event.Height {
		panic(fmt.Sprintf("tryFinalizeCommit() cs.Height: %v vs height: %v", stateData.Height, event.Height))
	}

	logger := cs.logger.With("height", event.Height)

	blockID, ok := stateData.Votes.Precommits(stateData.CommitRound).TwoThirdsMajority()
	if !ok || blockID.IsNil() {
		logger.Error("failed attempt to finalize commit; there was no +2/3 majority or +2/3 was for nil")
		return nil
	}

	if !stateData.ProposalBlock.HashesTo(blockID.Hash) {
		// TODO: this happens every time if we're not a validator (ugly logs)
		// TODO: ^^ wait, why does it matter that we're a validator?
		logger.Debug("failed attempt to finalize commit; we do not have the commit block",
			"proposal_block", tmstrings.LazyBlockHash(stateData.ProposalBlock),
			"commit_block", blockID.Hash,
		)
		return nil
	}

	cs.finalizeCommit(ctx, stateEvent.Ctrl, stateData, event.Height)
	return nil
}

// Increment height and goto cstypes.RoundStepNewHeight
func (cs *TryFinalizeCommitAction) finalizeCommit(ctx context.Context, ctrl *Controller, stateData *StateData, height int64) {
	logger := cs.logger.With("height", height)

	if stateData.Height != height || stateData.Step != cstypes.RoundStepApplyCommit {
		logger.Debug(
			"entering finalize commit step",
			"current", fmt.Sprintf("%v/%v/%v", stateData.Height, stateData.Round, stateData.Step),
		)
		return
	}

	blockID, ok := stateData.Votes.Precommits(stateData.CommitRound).TwoThirdsMajority()
	block, blockParts := stateData.ProposalBlock, stateData.ProposalBlockParts

	if !ok {
		panic("cannot finalize commit; commit does not have 2/3 majority")
	}
	if !blockParts.HasHeader(blockID.PartSetHeader) {
		panic("expected ProposalBlockParts header to be commit header")
	}
	if !block.HashesTo(blockID.Hash) {
		panic("cannot finalize commit; proposal block does not hash to commit hash")
	}

	logger.Info(
		"finalizing commit of block",
		"hash", tmstrings.LazyBlockHash(block),
		"root", block.AppHash,
		"num_txs", len(block.Txs),
	)

	precommits := stateData.Votes.Precommits(stateData.CommitRound)
	seenCommit := precommits.MakeCommit()
	_ = ctrl.Dispatch(ctx, &ApplyCommitEvent{Commit: seenCommit}, stateData)
}
