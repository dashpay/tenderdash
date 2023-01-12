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
}

func (cs *AddCommitCommand) Execute(ctx context.Context, behavior *Behavior, stateEvent StateEvent) (added any, err error) {
	event := stateEvent.Data.(AddCommitEvent)
	commit := event.Commit
	stateData := stateEvent.StateData
	// The commit is all good, let's apply it to the state
	err = behavior.updateProposalBlockAndParts(stateData, commit.BlockID)
	if err != nil {
		return nil, err
	}

	stateData.updateRoundStep(stateData.Round, cstypes.RoundStepApplyCommit)
	stateData.CommitRound = commit.Round
	stateData.CommitTime = tmtime.Now()
	behavior.newStep(stateData.RoundState)

	// The commit is all good, let's apply it to the state
	behavior.ApplyCommit(ctx, stateData, ApplyCommitEvent{Commit: commit})

	// This will relay the commit to peers
	if err := cs.eventPublisher.PublishCommitEvent(commit); err != nil {
		return false, fmt.Errorf("error adding commit: %w", err)
	}
	if stateData.bypassCommitTimeout() {
		_ = behavior.EnterNewRound(ctx, stateData, EnterNewRoundEvent{Height: stateData.Height})
	}
	return true, nil
}
