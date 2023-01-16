package consensus

import (
	"context"
	"fmt"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	tmtime "github.com/tendermint/tendermint/libs/time"
	"github.com/tendermint/tendermint/types"
)

type AddCommitEvent struct {
	Commit *types.Commit
}

type AddCommitCommand struct {
	eventPublisher *EventPublisher
	statsQueue     *chanQueue[msgInfo]
}

func (cs *AddCommitCommand) Execute(ctx context.Context, behavior *Behavior, stateEvent StateEvent) error {
	event := stateEvent.Data.(AddCommitEvent)
	commit := event.Commit
	stateData := stateEvent.StateData
	// The commit is all good, let's apply it to the state
	err := behavior.updateProposalBlockAndParts(stateData, commit.BlockID)
	if err != nil {
		return err
	}

	stateData.updateRoundStep(stateData.Round, cstypes.RoundStepApplyCommit)
	stateData.CommitRound = commit.Round
	stateData.CommitTime = tmtime.Now()
	behavior.newStep(stateData.RoundState)

	// The commit is all good, let's apply it to the state
	_ = behavior.ApplyCommit(ctx, stateData, ApplyCommitEvent{Commit: commit})

	// This will relay the commit to peers
	err = cs.eventPublisher.PublishCommitEvent(commit)
	if err != nil {
		return fmt.Errorf("error adding commit: %w", err)
	}
	if stateData.bypassCommitTimeout() {
		_ = behavior.EnterNewRound(ctx, stateData, EnterNewRoundEvent{Height: stateData.Height})
	}
	_ = cs.statsQueue.send(ctx, msgInfoFromCtx(ctx))
	return nil
}
