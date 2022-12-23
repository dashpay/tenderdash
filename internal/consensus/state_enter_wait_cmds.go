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
func (cs *EnterPrecommitWaitCommand) Execute(ctx context.Context, behavior *Behavior, stateEvent StateEvent) (any, error) {
	appState := stateEvent.AppState
	event := stateEvent.Data.(EnterPrecommitWaitEvent)
	height, round := event.Height, event.Round
	logger := cs.logger.With("new_height", height, "new_round", round)

	if appState.Height != height || round < appState.Round || (appState.Round == round && appState.TriggeredTimeoutPrecommit) {
		logger.Debug("entering precommit wait step with invalid args",
			"triggered_timeout", appState.TriggeredTimeoutPrecommit,
			"height", appState.Height,
			"round", appState.Round)
		return nil, nil
	}

	if !appState.Votes.Precommits(round).HasTwoThirdsAny() {
		panic(fmt.Sprintf(
			"entering precommit wait step (%v/%v), but precommits does not have any +2/3 votes",
			height, round,
		))
	}

	logger.Debug("entering precommit wait step",
		"height", appState.Height,
		"round", appState.Round,
		"step", appState.Step)

	defer func() {
		// Done enterPrecommitWait:
		appState.TriggeredTimeoutPrecommit = true
		behavior.newStep(appState.RoundState)
		// TODO PERSIST AppState
	}()

	// wait for some more precommits; enterNewRoundCommand
	behavior.ScheduleTimeout(appState.voteTimeout(round), height, round, cstypes.RoundStepPrecommitWait)
	return nil, nil
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
func (cs *EnterPrevoteWaitCommand) Execute(ctx context.Context, behavior *Behavior, stateEvent StateEvent) (any, error) {
	appState := stateEvent.AppState
	event := stateEvent.Data.(EnterPrevoteWaitEvent)
	height, round := event.Height, event.Round

	logger := cs.logger.With("height", height, "round", round)

	if appState.Height != height || round < appState.Round || (appState.Round == round && cstypes.RoundStepPrevoteWait <= appState.Step) {
		logger.Debug("entering prevote wait step with invalid args",
			"height", appState.Height,
			"round", appState.Round,
			"step", appState.Step)
		return nil, nil
	}

	if !appState.Votes.Prevotes(round).HasTwoThirdsAny() {
		panic(fmt.Sprintf(
			"entering prevote wait step (%v/%v), but prevotes does not have any +2/3 votes",
			height, round,
		))
	}

	logger.Debug("entering prevote wait step",
		"height", appState.Height,
		"round", appState.Round,
		"step", appState.Step)

	defer func() {
		// Done enterPrevoteWait:
		appState.updateRoundStep(round, cstypes.RoundStepPrevoteWait)
		behavior.newStep(appState.RoundState)
	}()

	// Wait for some more prevotes; enterPrecommit
	behavior.ScheduleTimeout(appState.voteTimeout(round), height, round, cstypes.RoundStepPrevoteWait)
	return nil, nil
}
