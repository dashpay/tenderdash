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

// GetType returns EnterPrevoteType event-type
func (e *EnterPrevoteEvent) GetType() EventType {
	return EnterPrevoteType
}

// EnterPrevoteAction ...
// Enter: `timeoutPropose` after entering Propose.
// Enter: proposal block and POL is ready.
// If we received a valid proposal within this round and we are not locked on a block,
// we will prevote for block.
// Otherwise, if we receive a valid proposal that matches the block we are
// locked on or matches a block that received a POL in a round later than our
// locked round, prevote for the proposal, otherwise vote nil.
type EnterPrevoteAction struct {
	logger         log.Logger
	eventPublisher *EventPublisher
	prevoter       Prevoter
}

// Execute ...
func (c *EnterPrevoteAction) Execute(ctx context.Context, statEvent StateEvent) error {
	epe := statEvent.Data.(*EnterPrevoteEvent)
	stateData := statEvent.StateData
	height := epe.Height
	round := epe.Round

	logger := c.logger.With("height", height, "round", round)

	if stateData.Height != height || round < stateData.Round || (stateData.Round == round && cstypes.RoundStepPrevote <= stateData.Step) {
		logger.Debug("entering prevote step with invalid args",
			"height", stateData.Height,
			"round", stateData.Round,
			"step", stateData.Step)
		return nil
	}

	defer func() {
		// Done enterPrevote:
		stateData.updateRoundStep(round, cstypes.RoundStepPrevote)
		c.eventPublisher.PublishNewRoundStepEvent(stateData.RoundState)
	}()

	logger.Debug("entering prevote step",
		"height", stateData.Height,
		"round", stateData.Round,
		"step", stateData.Step)

	// Sign and broadcast vote as necessary

	// Once `addVote` hits any +2/3 prevotes, we will go to PrevoteWait
	// (so we have more time to try and collect +2/3 prevotes for a single block)
	return c.prevoter.Do(ctx, stateData)
}
