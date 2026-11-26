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

// TryFinalizeCommitAction finalizes the height when this node holds the block
// and +2/3 precommits for it, unless the application rejects the commit built
// from those precommits.
type TryFinalizeCommitAction struct {
	logger log.Logger
	// create and execute blocks
	blockExec  *blockExecutor
	blockStore sm.BlockStore
	metrics    *Metrics
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

	// The block alone: not having it here is routine, so this returns quietly
	// where the same question panics in applyCommit below.
	if !stateData.ProposalBlock.HashesTo(blockID.Hash) {
		// TODO: this happens every time if we're not a validator (ugly logs)
		// TODO: ^^ wait, why does it matter that we're a validator?
		logger.Trace("failed attempt to finalize commit; we do not have the commit block",
			"proposal_block", tmstrings.LazyBlockHash(stateData.ProposalBlock),
			"commit_block", blockID.Hash,
		)
		return nil
	}

	cs.finalizeCommit(ctx, stateEvent.Ctrl, stateData, event.Height)
	return nil
}

// finalizeCommit applies the commit built from this node's own precommits, which
// moves to the next height. If the application rejects that commit, nothing is
// persisted and the node stays in RoundStepApplyCommit until a peer's commit
// arrives.
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

	// Decomposed rather than asked through holdsProposalBlock: each arm names
	// which half broke, and that name is all the operator gets from a panic.
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
		"block", block,
	)

	precommits := stateData.Votes.Precommits(stateData.CommitRound)
	seenCommit := precommits.MakeCommit()
	// The application must accept the extensions before the block is saved.
	// This commit is built from precommits this node verified itself, so a
	// rejection means the application disagrees with its own votes. Nothing is
	// persisted and the node stays at this height, where a peer's commit can
	// still finish it; a panic would only restart into the same commit.
	err := cs.blockExec.refuseUnprocessedCommit(&stateData.RoundState, seenCommit)
	if err == nil {
		cs.blockExec.mustEnsureProcess(ctx, &stateData.RoundState, seenCommit.Round)
		err = cs.blockExec.verifyCommitExtensions(ctx, &stateData.RoundState, seenCommit)
	}
	if err != nil {
		cs.metrics.CommitVerifyFailures.With("reason", CommitVerifyFailureReason(err)).Add(1)
		logger.Error("application rejected the commit of this node's own precommits; waiting for a peer's commit",
			"commit_round", seenCommit.Round, "error", err)
		return
	}
	_ = ctrl.Dispatch(ctx, &ApplyCommitEvent{Commit: seenCommit}, stateData)
}
