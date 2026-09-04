package consensus

import (
	"context"
	"fmt"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/libs/log"
	tmtime "github.com/dashpay/tenderdash/libs/time"
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
	// other block, having pointed the part set at the committed one instead. There
	// is nothing to apply until that block arrives, and its completing part
	// dispatches this event again.
	if stateData.ProposalBlock == nil {
		log.FromCtxOrNop(ctx).Debug("commit is for a block we do not have yet; waiting for it",
			"height", commit.Height,
			"round", commit.Round,
			"commit_block", commit.BlockID.Hash,
		)
		return nil
	}

	stateData.updateRoundStep(stateData.Round, cstypes.RoundStepApplyCommit)
	stateData.CommitRound = commit.Round
	stateData.CommitTime = tmtime.Now()
	c.eventPublisher.PublishNewRoundStepEvent(stateData.RoundState)

	// The commit is all good, let's apply it to the state
	_ = stateEvent.Ctrl.Dispatch(ctx, &ApplyCommitEvent{Commit: commit}, stateData)

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
