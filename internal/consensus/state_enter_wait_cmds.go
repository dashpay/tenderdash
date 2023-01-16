package consensus

import (
	"context"
	"fmt"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	"github.com/tendermint/tendermint/libs/log"
)

type EnterPrecommitWaitEvent struct {
	Height int64
	Round  int32
}

// EnterPrecommitWaitCommand ...
// Enter: any +2/3 precommits for next round.
type EnterPrecommitWaitCommand struct {
	logger log.Logger
}

// Execute ...
// Enter: any +2/3 precommits for next round.
func (cs *EnterPrecommitWaitCommand) Execute(ctx context.Context, behavior *Behavior, stateEvent StateEvent) error {
	stateData := stateEvent.StateData
	event := stateEvent.Data.(EnterPrecommitWaitEvent)
	height, round := event.Height, event.Round
	logger := cs.logger.With("new_height", height, "new_round", round)

	if stateData.Height != height || round < stateData.Round || (stateData.Round == round && stateData.TriggeredTimeoutPrecommit) {
		logger.Debug("entering precommit wait step with invalid args",
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
		behavior.newStep(stateData.RoundState)
		// TODO PERSIST StateData
	}()

	// wait for some more precommits; enterNewRoundCommand
	behavior.ScheduleTimeout(stateData.voteTimeout(round), height, round, cstypes.RoundStepPrecommitWait)
	return nil
}

type EnterPrevoteWaitEvent struct {
	Height int64
	Round  int32
}

// EnterPrevoteWaitCommand ...
// Enter: any +2/3 prevotes at next round.
type EnterPrevoteWaitCommand struct {
	logger log.Logger
}

// Execute ...
// Enter: any +2/3 prevotes at next round.
func (cs *EnterPrevoteWaitCommand) Execute(ctx context.Context, behavior *Behavior, stateEvent StateEvent) error {
	stateData := stateEvent.StateData
	event := stateEvent.Data.(EnterPrevoteWaitEvent)
	height, round := event.Height, event.Round

	logger := cs.logger.With("height", height, "round", round)

	if stateData.Height != height || round < stateData.Round || (stateData.Round == round && cstypes.RoundStepPrevoteWait <= stateData.Step) {
		logger.Debug("entering prevote wait step with invalid args",
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
		behavior.newStep(stateData.RoundState)
	}()

	// Wait for some more prevotes; enterPrecommit
	behavior.ScheduleTimeout(stateData.voteTimeout(round), height, round, cstypes.RoundStepPrevoteWait)
	return nil
}
