package consensus

import (
	"context"
	"fmt"

	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/types"
)

type ApplyCommitEvent struct {
	Commit *types.Commit
}

type ApplyCommitCommand struct {
	logger log.Logger
	// store blocks and commits
	blockStore sm.BlockStore
	// create and execute blocks
	blockExec *blockExecutor
	wal       WALWriteFlusher
}

func (cs *ApplyCommitCommand) Execute(ctx context.Context, behaviour *Behaviour, stateEvent StateEvent) (any, error) {
	event := stateEvent.Data.(ApplyCommitEvent)
	appState := stateEvent.AppState
	commit := event.Commit
	cs.logger.Info("applying commit", "commit", commit)

	block, blockParts := appState.ProposalBlock, appState.ProposalBlockParts

	height := appState.Height
	round := appState.Round

	if commit != nil {
		height = commit.Height
		round = commit.Round
	}

	cs.blockExec.processOrPanic(ctx, appState, round)
	cs.blockExec.validateOrPanic(ctx, appState)

	// Save to blockStore
	if commit != nil {
		cs.blockStore.SaveBlock(block, blockParts, commit)
	}

	// Write EndHeightMessage{} for this height, implying that the blockstore
	// has saved the block.
	//
	// If we crash before writing this EndHeightMessage{}, we will recover by
	// running ApplyBlock during the ABCI handshake when we restart.  If we
	// didn't save the block to the blockstore before writing
	// EndHeightMessage{}, we'd have to change WAL replay -- currently it
	// complains about replaying for heights where an #ENDHEIGHT entry already
	// exists.
	//
	// Either way, the State should not be resumed until we
	// successfully call ApplyBlock (ie. later here, or in Handshake after
	// restart).
	endMsg := EndHeightMessage{height}
	if err := cs.wal.WriteSync(endMsg); err != nil { // NOTE: fsync
		panic(fmt.Errorf(
			"failed to write %v msg to consensus WAL due to %w; check your file system and restart the node",
			endMsg, err,
		))
	}

	// Create a copy of the state for staging and an event cache for txs.
	stateCopy, err := cs.blockExec.finalize(ctx, appState, commit)
	if err != nil {
		cs.logger.Error("failed to apply block", "err", err)
		return nil, nil
	}

	lastBlockMeta := cs.blockStore.LoadBlockMeta(height - 1)

	// must be called before we update state
	behaviour.RecordMetrics(appState, height, block, lastBlockMeta)

	// NewHeightStep!
	appState.updateToState(stateCopy, commit)
	err = appState.Save()
	if err != nil {
		return nil, err
	}

	behaviour.newStep(appState.RoundState)

	// cs.StartTime is already set.
	// Schedule Round0 to start soon.
	behaviour.ScheduleRound0(appState.RoundState)

	// By here,
	// * cs.Height has been increment to height+1
	// * cs.Step is now cstypes.RoundStepNewHeight
	// * cs.StartTime is set to when we will start round0.
	return nil, nil
}
