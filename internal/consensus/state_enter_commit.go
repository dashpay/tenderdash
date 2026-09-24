package consensus

import (
	"context"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/libs/log"
)

type EnterCommitEvent struct {
	Height      int64
	CommitRound int32
}

// GetType returns EnterCommitType event-type
func (e *EnterCommitEvent) GetType() EventType {
	return EnterCommitType
}

// EnterCommitAction ...
// Enter: +2/3 precommits for block
type EnterCommitAction struct {
	logger          log.Logger
	eventPublisher  *EventPublisher
	proposalUpdater *proposalUpdater
	metrics         *Metrics
}

// Execute ...
func (c *EnterCommitAction) Execute(ctx context.Context, stateEvent StateEvent) error {
	event := stateEvent.Data.(*EnterCommitEvent)
	stateData := stateEvent.StateData
	height := event.Height
	commitRound := event.CommitRound
	logger := c.logger.With("new_height", height, "commit_round", commitRound)

	if stateData.Height != height || cstypes.RoundStepApplyCommit <= stateData.Step {
		// this is quite common event
		logger.Trace("entering commit step with invalid args",
			"height", stateData.Height,
			"round", stateData.Round,
			"step", stateData.Step)
		return nil
	}

	logger.Debug("entering commit step",
		"height", stateData.Height,
		"round", stateData.Round,
		"step", stateData.Step)

	defer func() {
		// Done enterCommit:
		// keep c.Round the same, commitRound points to the right Precommits set.
		stateData.enterApplyCommit(commitRound)
		c.eventPublisher.PublishNewRoundStepEvent(stateData.RoundState)

		// Maybe finalize immediately. A failure has already been handled where it
		// happened; it is logged here because the vote that got us here is not
		// at fault.
		if err := stateEvent.Ctrl.Dispatch(ctx, &TryFinalizeCommitEvent{Height: height}, stateData); err != nil {
			logger.Error("failed to finalize commit", "error", err)
		}
	}()

	blockID, ok := stateData.Votes.Precommits(commitRound).TwoThirdsMajority()
	if !ok {
		panic("RunActionCommit() expects +2/3 precommits")
	}

	return c.proposalUpdater.updateStateData(stateData, blockID)
}
