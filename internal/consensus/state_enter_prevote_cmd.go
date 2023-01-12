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
func (cs *EnterPrevoteCommand) Execute(ctx context.Context, behavior *Behavior, event StateEvent) (any, error) {
	epe := event.Data.(EnterPrevoteEvent)
	stateData := event.StateData
	height := epe.Height
	round := epe.Round

	logger := cs.logger.With("height", height, "round", round)

	if stateData.Height != height || round < stateData.Round || (stateData.Round == round && cstypes.RoundStepPrevote <= stateData.Step) {
		logger.Debug("entering prevote step with invalid args",
			"height", stateData.Height,
			"round", stateData.Round,
			"step", stateData.Step)
		return nil, nil
	}

	defer func() {
		// Done enterPrevote:
		stateData.updateRoundStep(round, cstypes.RoundStepPrevote)
		behavior.newStep(stateData.RoundState)
	}()

	logger.Debug("entering prevote step",
		"height", stateData.Height,
		"round", stateData.Round,
		"step", stateData.Step)

	// Sign and broadcast vote as necessary
	prevoteEvent := DoPrevoteEvent{
		Height:         height,
		Round:          round,
		AllowOldBlocks: epe.AllowOldBlocks,
	}
	err := behavior.DoPrevote(ctx, stateData, prevoteEvent)
	if err != nil {
		return nil, err
	}

	// Once `addVote` hits any +2/3 prevotes, we will go to PrevoteWait
	// (so we have more time to try and collect +2/3 prevotes for a single block)
	return nil, nil
}
