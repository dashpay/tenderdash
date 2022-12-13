package consensus

import (
	"context"
	"time"

	"github.com/tendermint/tendermint/config"
	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	"github.com/tendermint/tendermint/libs/log"
	tmtime "github.com/tendermint/tendermint/libs/time"
)

// EnterNewRoundEvent ...
type EnterNewRoundEvent struct {
	Height int64
	Round  int32
}

// EnterNewRoundCommand ...
// Enter: `timeoutNewHeight` by startTime (commitTime+timeoutCommit),
//
//	or, if SkipTimeoutCommit==true, after receiving all precommits from (height,round-1)
//
// Enter: `timeoutPrecommits` after any +2/3 precommits from (height,round-1)
// Enter: +2/3 precommits for nil at (height,round-1)
// Enter: +2/3 prevotes any or +2/3 precommits for block or any from (height, round)
// Enter: A valid commit came in from a future round
// NOTE: cs.StartTime was already set for height.
type EnterNewRoundCommand struct {
	logger         log.Logger
	config         *config.ConsensusConfig
	eventPublisher *EventPublisher
}

// Execute ...
func (cs *EnterNewRoundCommand) Execute(ctx context.Context, behaviour *Behaviour, stateEvent StateEvent) (any, error) {
	event := stateEvent.Data.(EnterNewRoundEvent)
	appState := stateEvent.AppState
	height := event.Height
	round := event.Round
	// TODO: remove panics in this function and return an error

	logger := cs.logger.With("height", height, "round", round)

	if appState.Height != height || round < appState.Round || (appState.Round == round && appState.Step != cstypes.RoundStepNewHeight) {
		logger.Debug("entering new round with invalid args",
			"height", appState.Height,
			"round", appState.Round,
			"step", appState.Step)
		return nil, nil
	}

	if now := tmtime.Now(); appState.StartTime.After(now) {
		logger.Debug("need to set a buffer and log message here for sanity", "start_time", appState.StartTime, "now", now)
	}

	logger.Debug("entering new round",
		"height", appState.Height,
		"round", appState.Round,
		"step", appState.Step)

	// increment validators if necessary
	validators := appState.Validators
	if appState.Round < round {
		validators = validators.Copy()
		validators.IncrementProposerPriority(round - appState.Round)
	}

	// Setup new round
	// we don't fire newStep for this step,
	// but we fire an event, so update the round step first
	appState.updateRoundStep(round, cstypes.RoundStepNewRound)
	appState.Validators = validators
	if round == 0 {
		// We've already reset these upon new height,
		// and meanwhile we might have received a proposal
		// for round 0.
	} else {
		logger.Debug("resetting proposal info")
		appState.Proposal = nil
		appState.ProposalReceiveTime = time.Time{}
		appState.ProposalBlock = nil
		appState.ProposalBlockParts = nil
	}

	appState.Votes.SetRound(round + 1) // also track next round (round+1) to allow round-skipping
	appState.TriggeredTimeoutPrecommit = false
	err := appState.Save()
	if err != nil {
		return nil, err
	}

	cs.eventPublisher.PublishNewRoundEvent(appState.NewRoundEvent())
	// Wait for txs to be available in the mempool
	// before we enterPropose in round 0. If the last block changed the app hash,
	waitForTxs := cs.config.WaitForTxs() && round == 0 && appState.state.InitialHeight != appState.Height
	if waitForTxs {
		if cs.config.CreateEmptyBlocksInterval > 0 {
			behaviour.ScheduleTimeout(cs.config.CreateEmptyBlocksInterval, height, round, cstypes.RoundStepNewRound)
		}
	} else if !cs.config.DontAutoPropose {
		// DontAutoPropose should always be false, except for
		// specific tests where proposals are created manually
		err = behaviour.EnterPropose(ctx, appState, EnterProposeEvent{Height: height, Round: round})
		if err != nil {
			return nil, err
		}
	}
	return nil, nil
}
