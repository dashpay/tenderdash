package consensus

import (
	"context"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	"github.com/tendermint/tendermint/libs/log"
	tmtime "github.com/tendermint/tendermint/libs/time"
)

type EnterCommitEvent struct {
	Height      int64
	CommitRound int32
}

// GetType returns EnterCommitType event-type
func (e *EnterCommitEvent) GetType() EventType {
	return EnterCommitType
}

// EnterCommitCommand ...
// Enter: +2/3 precommits for block
type EnterCommitCommand struct {
	logger          log.Logger
	eventPublisher  *EventPublisher
	proposalUpdater *proposalUpdater
	metrics         *Metrics
}

// Execute ...
func (c *EnterCommitCommand) Execute(ctx context.Context, stateEvent StateEvent) error {
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
		_ = stateEvent.FSM.Dispatch(ctx, &TryFinalizeCommitEvent{Height: height}, stateData)
	}()

	blockID, ok := stateData.Votes.Precommits(commitRound).TwoThirdsMajority()
	if !ok {
		panic("RunActionCommit() expects +2/3 precommits")
	}

	return c.proposalUpdater.updateStateData(stateData, blockID)
}

func (c *EnterCommitCommand) Subscribe(observer *Observer) {
	observer.Subscribe(SetMetrics, func(a any) error {
		c.metrics = a.(*Metrics)
		return nil
	})
}
