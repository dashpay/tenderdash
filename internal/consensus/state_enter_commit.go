package consensus

import (
	"context"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/libs/log"
	tmtime "github.com/dashpay/tenderdash/libs/time"
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
		logger.Debug("entering commit step with invalid args",
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
		stateData.updateRoundStep(stateData.Round, cstypes.RoundStepApplyCommit)
		stateData.CommitRound = commitRound
		stateData.CommitTime = tmtime.Now()
		c.eventPublisher.PublishNewRoundStepEvent(stateData.RoundState)

		// Maybe finalize immediately.
		_ = stateEvent.Ctrl.Dispatch(ctx, &TryFinalizeCommitEvent{Height: height}, stateData)
	}()

	blockID, ok := stateData.Votes.Precommits(commitRound).TwoThirdsMajority()
	if !ok {
		panic("RunActionCommit() expects +2/3 precommits")
	}

	return c.proposalUpdater.updateStateData(stateData, blockID)
}
