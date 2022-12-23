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
	appState := stateEvent.AppState
	// The commit is all good, let's apply it to the state
	err = behavior.updateProposalBlockAndParts(appState, commit.BlockID)
	if err != nil {
		return nil, err
	}

	appState.updateRoundStep(appState.Round, cstypes.RoundStepApplyCommit)
	appState.CommitRound = commit.Round
	appState.CommitTime = tmtime.Now()
	behavior.newStep(appState.RoundState)

	// The commit is all good, let's apply it to the state
	behavior.ApplyCommit(ctx, appState, ApplyCommitEvent{Commit: commit})

	// This will relay the commit to peers
	if err := cs.eventPublisher.PublishCommitEvent(commit); err != nil {
		return false, fmt.Errorf("error adding commit: %w", err)
	}
	if appState.bypassCommitTimeout() {
		_ = behavior.EnterNewRound(ctx, appState, EnterNewRoundEvent{Height: appState.Height})
	}
	return true, nil
}
