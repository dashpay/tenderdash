package consensus

import (
	"context"
	"fmt"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	tmstrings "github.com/tendermint/tendermint/internal/libs/strings"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/libs/log"
)

type TryFinalizeCommitEvent struct {
	Height int64
}

// TryFinalizeCommitCommand ...
// If we have the block AND +2/3 commits for it, finalize.
type TryFinalizeCommitCommand struct {
	logger log.Logger
	// create and execute blocks
	blockExec  *blockExecutor
	blockStore sm.BlockStore
}

// Execute ...
func (cs *TryFinalizeCommitCommand) Execute(ctx context.Context, behavior *Behavior, stateEvent StateEvent) error {
	event := stateEvent.Data.(TryFinalizeCommitEvent)
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

	cs.finalizeCommit(ctx, behavior, stateData, event.Height)
	return nil
}

// Increment height and goto cstypes.RoundStepNewHeight
func (cs *TryFinalizeCommitCommand) finalizeCommit(ctx context.Context, behavior *Behavior, stateData *StateData, height int64) {
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

	// Save to blockStore.
	if cs.blockStore.Height() < block.Height {
		// NOTE: the seenCommit is local justification to commit this block,
		// but may differ from the LastPrecommits included in the next block
		precommits := stateData.Votes.Precommits(stateData.CommitRound)
		seenCommit := precommits.MakeCommit()
		behavior.ApplyCommit(ctx, stateData, ApplyCommitEvent{Commit: seenCommit})
		return
	}
	// Happens during replay if we already saved the block but didn't commit
	logger.Debug("calling tryFinalizeCommit on already stored block", "height", block.Height)
	// Todo: do we need this?
	behavior.ApplyCommit(ctx, stateData, ApplyCommitEvent{})
}
