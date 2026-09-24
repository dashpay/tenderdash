package consensus

import (
	"context"
	"fmt"

	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

type AddCommitEvent struct {
	Commit *types.Commit
}

// GetType returns AddCommitType event-type
func (e *AddCommitEvent) GetType() EventType {
	return AddCommitType
}

type AddCommitAction struct {
	eventPublisher  *EventPublisher
	statsQueue      *chanQueue[msgInfo]
	proposalUpdater *proposalUpdater
}

func (c *AddCommitAction) Execute(ctx context.Context, stateEvent StateEvent) error {
	event := stateEvent.Data.(*AddCommitEvent)
	commit := event.Commit
	stateData := stateEvent.StateData
	// The commit is all good, let's apply it to the state
	err := c.proposalUpdater.updateStateData(stateData, commit.BlockID)
	if err != nil {
		return err
	}

	// updateStateData clears ProposalBlock when the round state was holding some
	// other block, having pointed the part set at the committed one instead.
	// Both callers normally rule this out: TryAddCommit dispatches only for a
	// held block, and a completing part dispatches the commit parked for it.
	// Should it happen, the commit is parked here, because a completing part
	// dispatches this event again only for stateData.Commit.
	if stateData.ProposalBlock == nil {
		stateData.Commit = commit
		log.FromCtxOrNop(ctx).Debug("commit is for a block we do not have yet; waiting for it",
			"height", commit.Height,
			"round", commit.Round,
			"commit_block", commit.BlockID.Hash,
		)
		return nil
	}

	// The commit is all good, let's apply it to the state
	applyEvent := &ApplyCommitEvent{Commit: commit}
	if err := stateEvent.Ctrl.Dispatch(ctx, applyEvent, stateData); err != nil {
		return err
	}
	if applyEvent.Rejected {
		return nil
	}

	// This will relay the commit to peers
	err = c.eventPublisher.PublishCommitEvent(commit)
	if err != nil {
		return fmt.Errorf("error adding commit: %w", err)
	}

	// We go to next round, as in Tenderdash we don't need to wait for new commits
	_ = stateEvent.Ctrl.Dispatch(ctx, &EnterNewRoundEvent{Height: stateData.Height}, stateData)

	_ = c.statsQueue.send(ctx, msgInfoFromCtx(ctx))
	return nil
}
