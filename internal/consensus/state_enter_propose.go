package consensus

import (
	"context"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	"github.com/tendermint/tendermint/libs/events"
	"github.com/tendermint/tendermint/libs/log"
	tmtime "github.com/tendermint/tendermint/libs/time"
)

type EnterProposeEvent struct {
	Height int64
	Round  int32
}

// GetType returns EnterProposeType event-type
func (e *EnterProposeEvent) GetType() EventType {
	return EnterProposeType
}

// EnterProposeAction ...
// Enter (CreateEmptyBlocks): from enterNewRound(height,round)
// Enter (CreateEmptyBlocks, CreateEmptyBlocksInterval > 0 ):
// after enterNewRound(height,round), after timeout of CreateEmptyBlocksInterval
// Enter (!CreateEmptyBlocks) : after enterNewRound(height,round), once txs are in the mempool
// Caller should hold cs.mtx lock
type EnterProposeAction struct {
	logger         log.Logger
	privValidator  privValidator
	msgInfoQueue   *msgInfoQueue
	wal            WALWriteFlusher
	replayMode     bool
	metrics        *Metrics
	blockExec      *blockExecutor
	scheduler      *roundScheduler
	eventPublisher *EventPublisher
}

func (c *EnterProposeAction) Execute(ctx context.Context, stateEvent StateEvent) error {
	event := stateEvent.Data.(*EnterProposeEvent)
	stateData := stateEvent.StateData
	height := event.Height
	round := event.Round

	logger := c.logger.With("height", height, "round", round)

	if stateData.Height != height || round < stateData.Round || (stateData.Round == round && cstypes.RoundStepPropose <= stateData.Step) {
		logger.Debug("entering propose step with invalid args", "step", stateData.Step)
		return nil
	}

	// If this validator is the proposer of this round, and the previous block time is later than
	// our local clock time, wait to propose until our local clock time has passed the block time.
	if stateData.isProposer(c.privValidator.ProTxHash) {
		pwt := proposerWaitTime(tmtime.Now(), stateData.state.LastBlockTime)
		if pwt > 0 {
			c.logger.Debug("enter propose: latest block is newer, sleeping",
				"duration", pwt.String(),
				"last_block_time", stateData.state.LastBlockTime,
				"now", tmtime.Now(),
			)
			c.scheduler.ScheduleTimeout(pwt, height, round, cstypes.RoundStepNewRound)
			return nil
		}
	}

	logger.Debug("entering propose step",
		"height", stateData.Height,
		"round", stateData.Round,
		"step", stateData.Step)

	defer func() {
		// Done enterPropose:
		stateData.updateRoundStep(round, cstypes.RoundStepPropose)
		c.eventPublisher.PublishNewRoundStepEvent(stateData.RoundState)

		// If we have the whole proposal + POL, then goto Prevote now.
		// else, we'll enterPrevote when the rest of the proposal is received (in AddProposalBlockPart),
		// or else after timeoutPropose
		if stateData.isProposalComplete() {
			_ = stateEvent.Ctrl.Dispatch(ctx, &EnterPrevoteEvent{Height: height, Round: round}, stateData)
		}
	}()

	// If we don't get the proposal and all block parts quick enough, enterPrevote
	c.scheduler.ScheduleTimeout(stateData.proposeTimeout(round), height, round, cstypes.RoundStepPropose)

	// Nothing more to do if we're not a validator
	if c.privValidator.IsZero() {
		logger.Debug("propose step; not proposing since node is not a validator")
		return nil
	}

	proTxHash := c.privValidator.ProTxHash

	// if not a validator, we're done
	if !stateData.Validators.HasProTxHash(proTxHash) {
		logger.Debug("propose step; not proposing since node is not in the validator set",
			"proTxHash", proTxHash.ShortString(),
			"vals", stateData.Validators)
		return nil
	}

	if stateData.isProposer(proTxHash) {
		logger.Debug("propose step; our turn to propose",
			"proposer", proTxHash.ShortString(),
			"privValidator", c.privValidator,
		)
		_ = stateEvent.Ctrl.Dispatch(ctx, &DecideProposalEvent{Height: height, Round: round}, stateData)
	} else {
		logger.Debug("propose step; not our turn to propose",
			"proposer",
			stateData.Validators.GetProposer().ProTxHash,
			"privValidator",
			c.privValidator)
	}
	return nil
}

func (c *EnterProposeAction) subscribe(evsw events.EventSwitch) {
	const listenerID = "enterProposeAction"
	_ = evsw.AddListenerForEvent(listenerID, setReplayMode, func(a events.EventData) error {
		c.replayMode = a.(bool)
		return nil
	})
	_ = evsw.AddListenerForEvent(listenerID, setPrivValidator, func(a events.EventData) error {
		c.privValidator = a.(privValidator)
		return nil
	})
}
