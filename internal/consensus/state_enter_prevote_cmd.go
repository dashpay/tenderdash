package consensus

import (
	"context"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	"github.com/tendermint/tendermint/libs/log"
)

type EnterPrevoteEvent struct {
	Height         int64
	Round          int32
	AllowOldBlocks bool
}

// EnterPrevoteCommand ...
// Enter: `timeoutPropose` after entering Propose.
// Enter: proposal block and POL is ready.
// If we received a valid proposal within this round and we are not locked on a block,
// we will prevote for block.
// Otherwise, if we receive a valid proposal that matches the block we are
// locked on or matches a block that received a POL in a round later than our
// locked round, prevote for the proposal, otherwise vote nil.
type EnterPrevoteCommand struct {
	logger log.Logger
	//prevoter *Prevoter
}

// Execute ...
func (cs *EnterPrevoteCommand) Execute(ctx context.Context, behaviour *Behaviour, event StateEvent) (any, error) {
	epe := event.Data.(EnterPrevoteEvent)
	appState := event.AppState
	height := epe.Height
	round := epe.Round

	logger := cs.logger.With("height", height, "round", round)

	if appState.Height != height || round < appState.Round || (appState.Round == round && cstypes.RoundStepPrevote <= appState.Step) {
		logger.Debug("entering prevote step with invalid args",
			"height", appState.Height,
			"round", appState.Round,
			"step", appState.Step)
		return nil, nil
	}

	defer func() {
		// Done enterPrevote:
		appState.updateRoundStep(round, cstypes.RoundStepPrevote)
		behaviour.newStep(appState.RoundState)
	}()

	logger.Debug("entering prevote step",
		"height", appState.Height,
		"round", appState.Round,
		"step", appState.Step)

	// Sign and broadcast vote as necessary
	prevoteEvent := DoPrevoteEvent{
		Height:         height,
		Round:          round,
		AllowOldBlocks: epe.AllowOldBlocks,
	}
	err := behaviour.DoPrevote(ctx, appState, prevoteEvent)
	if err != nil {
		return nil, err
	}

	// Once `addVote` hits any +2/3 prevotes, we will go to PrevoteWait
	// (so we have more time to try and collect +2/3 prevotes for a single block)
	return nil, nil
}
