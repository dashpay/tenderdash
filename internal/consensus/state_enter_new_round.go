package consensus

import (
	"context"
	"time"

	"github.com/dashpay/tenderdash/config"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/libs/log"
	tmtime "github.com/dashpay/tenderdash/libs/time"
)

// EnterNewRoundEvent ...
type EnterNewRoundEvent struct {
	Height int64
	Round  int32
}

// GetType returns EnterNewRoundType event-type
func (e *EnterNewRoundEvent) GetType() EventType {
	return EnterNewRoundType
}

// EnterNewRoundAction ...
// Enter: `timeoutNewHeight` by startTime (commitTime+timeoutCommit),
//
//	or, if SkipTimeoutCommit==true, after receiving all precommits from (height,round-1)
//
// Enter: `timeoutPrecommits` after any +2/3 precommits from (height,round-1)
// Enter: +2/3 precommits for nil at (height,round-1)
// Enter: +2/3 prevotes any or +2/3 precommits for block or any from (height, round)
// Enter: A valid commit came in from a future round
// NOTE: cs.StartTime was already set for height.
type EnterNewRoundAction struct {
	logger         log.Logger
	config         *config.ConsensusConfig
	scheduler      *roundScheduler
	eventPublisher *EventPublisher
}

// Execute ...
func (c *EnterNewRoundAction) Execute(ctx context.Context, stateEvent StateEvent) error {
	event := stateEvent.Data.(*EnterNewRoundEvent)
	stateData := stateEvent.StateData
	height := event.Height
	round := event.Round
	// TODO: remove panics in this function and return an error

	logger := c.logger.With("height", height, "round", round)

	if stateData.Height != height || round < stateData.Round || (stateData.Round == round && stateData.Step != cstypes.RoundStepNewHeight) {
		// this is quite common event
		logger.Trace("entering new round with invalid args",
			"height", stateData.Height,
			"round", stateData.Round,
			"step", stateData.Step)
		return nil
	}

	if now := tmtime.Now(); stateData.StartTime.After(now) {
		logger.Trace("need to set a buffer and log message here for sanity", "start_time", stateData.StartTime, "now", now)
	}

	logger.Debug("entering new round",
		"height", stateData.Height,
		"round", stateData.Round,
		"step", stateData.Step)

	// increment validators if necessary
	validators := stateData.Validators

	// Setup new round
	// we don't fire newStep for this step,
	// but we fire an event, so update the round step first
	stateData.updateRoundStep(round, cstypes.RoundStepNewRound)
	stateData.Validators = validators
	if round == 0 {
		// We've already reset these upon new height,
		// and meanwhile we might have received a proposal
		// for round 0.
	} else {
		logger.Trace("resetting proposal info")
		stateData.Proposal = nil
		stateData.ProposalReceiveTime = time.Time{}
		stateData.ProposalBlock = nil
		stateData.ProposalBlockParts = nil
	}

	stateData.Votes.SetRound(round + 1) // also track next round (round+1) to allow round-skipping
	stateData.TriggeredTimeoutPrecommit = false
	err := stateData.Save()
	if err != nil {
		return err
	}

	c.eventPublisher.PublishNewRoundEvent(stateData.NewRoundEvent())
	// Wait for txs to be available in the mempool
	// before we enterPropose in round 0. If the last block changed the app hash,
	waitForTxs := c.config.WaitForTxs() && round == 0 && stateData.state.InitialHeight != stateData.Height
	if waitForTxs {
		if c.config.CreateEmptyBlocksInterval > 0 {
			c.scheduler.ScheduleTimeout(c.config.CreateEmptyBlocksInterval, height, round, cstypes.RoundStepNewRound)
		}
	} else if !c.config.DontAutoPropose {
		// DontAutoPropose should always be false, except for
		// specific tests where proposals are created manually
		err = stateEvent.Ctrl.Dispatch(ctx, &EnterProposeEvent{Height: height, Round: round}, stateData)
		if err != nil {
			return err
		}
	}
	return nil
}
