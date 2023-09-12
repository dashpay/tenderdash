package consensus

import (
	"context"
	"fmt"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/libs/log"
)

type EnterPrecommitWaitEvent struct {
	Height int64
	Round  int32
}

// GetType returns EnterPrecommitWaitType event-type
func (e *EnterPrecommitWaitEvent) GetType() EventType {
	return EnterPrecommitWaitType
}

// EnterPrecommitWaitAction ...
// Enter: any +2/3 precommits for next round.
type EnterPrecommitWaitAction struct {
	logger         log.Logger
	scheduler      *roundScheduler
	eventPublisher *EventPublisher
}

// Execute ...
// Enter: any +2/3 precommits for next round.
func (c *EnterPrecommitWaitAction) Execute(ctx context.Context, stateEvent StateEvent) error {
	stateData := stateEvent.StateData
	event := stateEvent.Data.(*EnterPrecommitWaitEvent)
	height, round := event.Height, event.Round
	logger := c.logger.With("new_height", height, "new_round", round)

	if stateData.Height != height || round < stateData.Round || (stateData.Round == round && stateData.TriggeredTimeoutPrecommit) {
		logger.Trace("entering precommit wait step with invalid args",
			"triggered_timeout", stateData.TriggeredTimeoutPrecommit,
			"height", stateData.Height,
			"round", stateData.Round)
		return nil
	}

	if !stateData.Votes.Precommits(round).HasTwoThirdsAny() {
		panic(fmt.Sprintf(
			"entering precommit wait step (%v/%v), but precommits does not have any +2/3 votes",
			height, round,
		))
	}

	logger.Debug("entering precommit wait step",
		"height", stateData.Height,
		"round", stateData.Round,
		"step", stateData.Step)

	defer func() {
		// Done enterPrecommitWait:
		stateData.TriggeredTimeoutPrecommit = true
		c.eventPublisher.PublishNewRoundStepEvent(stateData.RoundState)
		// TODO PERSIST StateData
	}()

	// wait for some more precommits; enterNewRound
	c.scheduler.ScheduleTimeout(stateData.voteTimeout(round), height, round, cstypes.RoundStepPrecommitWait)
	return nil
}

type EnterPrevoteWaitEvent struct {
	Height int64
	Round  int32
}

// GetType ...
func (e *EnterPrevoteWaitEvent) GetType() EventType {
	return EnterPrevoteWaitType
}

// EnterPrevoteWaitAction ...
// Enter: any +2/3 prevotes at next round.
type EnterPrevoteWaitAction struct {
	logger         log.Logger
	scheduler      *roundScheduler
	eventPublisher *EventPublisher
}

// Execute ...
// Enter: any +2/3 prevotes at next round.
func (c *EnterPrevoteWaitAction) Execute(ctx context.Context, stateEvent StateEvent) error {
	stateData := stateEvent.StateData
	event := stateEvent.Data.(*EnterPrevoteWaitEvent)
	height, round := event.Height, event.Round

	logger := c.logger.With("height", height, "round", round)

	if stateData.Height != height || round < stateData.Round || (stateData.Round == round && cstypes.RoundStepPrevoteWait <= stateData.Step) {
		logger.Trace("entering prevote wait step with invalid args",
			"height", stateData.Height,
			"round", stateData.Round,
			"step", stateData.Step)
		return nil
	}

	if !stateData.Votes.Prevotes(round).HasTwoThirdsAny() {
		panic(fmt.Sprintf(
			"entering prevote wait step (%v/%v), but prevotes does not have any +2/3 votes",
			height, round,
		))
	}

	logger.Debug("entering prevote wait step",
		"height", stateData.Height,
		"round", stateData.Round,
		"step", stateData.Step)

	defer func() {
		// Done enterPrevoteWait:
		stateData.updateRoundStep(round, cstypes.RoundStepPrevoteWait)
		c.eventPublisher.PublishNewRoundStepEvent(stateData.RoundState)
	}()

	// Wait for some more prevotes; enterPrecommit
	c.scheduler.ScheduleTimeout(stateData.voteTimeout(round), height, round, cstypes.RoundStepPrevoteWait)
	return nil
}
