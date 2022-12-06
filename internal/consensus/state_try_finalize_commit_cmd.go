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
	blockExec  *sm.BlockExecutor
	blockStore sm.BlockStore
}

// Execute ...
func (cs *TryFinalizeCommitCommand) Execute(ctx context.Context, behaviour *Behaviour, stateEvent StateEvent) (any, error) {
	event := stateEvent.Data.(TryFinalizeCommitEvent)
	appState := stateEvent.AppState
	if appState.Height != event.Height {
		panic(fmt.Sprintf("tryFinalizeCommit() cs.Height: %v vs height: %v", appState.Height, event.Height))
	}

	logger := cs.logger.With("height", event.Height)

	blockID, ok := appState.Votes.Precommits(appState.CommitRound).TwoThirdsMajority()
	if !ok || blockID.IsNil() {
		logger.Error("failed attempt to finalize commit; there was no +2/3 majority or +2/3 was for nil")
		return nil, nil
	}

	if !appState.ProposalBlock.HashesTo(blockID.Hash) {
		// TODO: this happens every time if we're not a validator (ugly logs)
		// TODO: ^^ wait, why does it matter that we're a validator?
		logger.Debug("failed attempt to finalize commit; we do not have the commit block",
			"proposal_block", tmstrings.LazyBlockHash(appState.ProposalBlock),
			"commit_block", blockID.Hash,
		)
		return nil, nil
	}

	if appState.CurrentRoundState.IsEmpty() {
		var err error
		// TODO: Check if using cs.Round here is correct
		appState.CurrentRoundState, err = cs.blockExec.ProcessProposal(ctx, appState.ProposalBlock, appState.Round, appState.state, true)
		if err != nil {
			panic(fmt.Errorf("couldn't call ProcessProposal abci method: %w", err))
		}
	}

	cs.finalizeCommit(ctx, behaviour, appState, event.Height)
	return nil, nil
}

// Increment height and goto cstypes.RoundStepNewHeight
func (cs *TryFinalizeCommitCommand) finalizeCommit(ctx context.Context, behaviour *Behaviour, appState *AppState, height int64) {
	logger := cs.logger.With("height", height)

	if appState.Height != height || appState.Step != cstypes.RoundStepApplyCommit {
		logger.Debug(
			"entering finalize commit step",
			"current", fmt.Sprintf("%v/%v/%v", appState.Height, appState.Round, appState.Step),
		)
		return
	}

	blockID, ok := appState.Votes.Precommits(appState.CommitRound).TwoThirdsMajority()
	block, blockParts := appState.ProposalBlock, appState.ProposalBlockParts

	if !ok {
		panic("cannot finalize commit; commit does not have 2/3 majority")
	}
	if !blockParts.HasHeader(blockID.PartSetHeader) {
		panic("expected ProposalBlockParts header to be commit header")
	}
	if !block.HashesTo(blockID.Hash) {
		panic("cannot finalize commit; proposal block does not hash to commit hash")
	}

	if err := cs.blockExec.ValidateBlockWithRoundState(ctx, appState.state, appState.CurrentRoundState, block); err != nil {
		panic(fmt.Errorf("+2/3 committed an invalid block %X: %w", appState.CurrentRoundState.AppHash, err))
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
		precommits := appState.Votes.Precommits(appState.CommitRound)
		seenCommit := precommits.MakeCommit()
		behaviour.ApplyCommit(ctx, appState, ApplyCommitEvent{Commit: seenCommit})
		return
	}
	// Happens during replay if we already saved the block but didn't commit
	logger.Debug("calling tryFinalizeCommit on already stored block", "height", block.Height)
	// Todo: do we need this?
	behaviour.ApplyCommit(ctx, appState, ApplyCommitEvent{})
}
