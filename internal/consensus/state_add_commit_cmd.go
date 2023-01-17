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

// GetType returns AddCommitType event-type
func (e *AddCommitEvent) GetType() EventType {
	return AddCommitType
}

type AddCommitCommand struct {
	eventPublisher  *EventPublisher
	statsQueue      *chanQueue[msgInfo]
	proposalUpdater *proposalUpdater
}

func (c *AddCommitCommand) Execute(ctx context.Context, stateEvent StateEvent) error {
	event := stateEvent.Data.(*AddCommitEvent)
	commit := event.Commit
	stateData := stateEvent.StateData
	// The commit is all good, let's apply it to the state
	err := c.proposalUpdater.updateStateData(stateData, commit.BlockID)
	if err != nil {
		return err
	}

	stateData.updateRoundStep(stateData.Round, cstypes.RoundStepApplyCommit)
	stateData.CommitRound = commit.Round
	stateData.CommitTime = tmtime.Now()
	c.eventPublisher.PublishNewRoundStepEvent(stateData.RoundState)

	// The commit is all good, let's apply it to the state
	_ = stateEvent.FSM.Dispatch(ctx, &ApplyCommitEvent{Commit: commit}, stateData)

	// This will relay the commit to peers
	err = c.eventPublisher.PublishCommitEvent(commit)
	if err != nil {
		return fmt.Errorf("error adding commit: %w", err)
	}
	if stateData.bypassCommitTimeout() {
		_ = stateEvent.FSM.Dispatch(ctx, &EnterNewRoundEvent{Height: stateData.Height}, stateData)
	}
	_ = c.statsQueue.send(ctx, msgInfoFromCtx(ctx))
	return nil
}
