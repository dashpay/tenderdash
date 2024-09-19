package consensus

import (
	"context"

	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/libs/log"
	tmtime "github.com/dashpay/tenderdash/libs/time"
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
	logger          log.Logger
	wal             WALFlusher
	scheduler       *roundScheduler
	eventPublisher  *EventPublisher
	proposalCreator cstypes.ProposalCreator
	replayMode      bool
}

func (c *EnterProposeAction) Execute(ctx context.Context, stateEvent StateEvent) error {
	event := stateEvent.Data.(*EnterProposeEvent)
	stateData := stateEvent.StateData
	height := event.Height
	round := event.Round

	logger := c.logger.With("height", height, "round", round)

	if stateData.Height != height ||
		round < stateData.Round ||
		(stateData.Round == round && cstypes.RoundStepPropose <= stateData.Step) {
		logger.Trace("entering propose step with invalid args", "step", stateData.Step)
		return nil
	}

	proTxHash := dash.MustProTxHashFromContext(ctx)
	isProposer := stateData.isProposer(proTxHash)

	// If this validator is the proposer of this round, and the previous block time is later than
	// our local clock time, wait to propose until our local clock time has passed the block time.
	if isProposer {
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

	if !isProposer {
		prop, err := stateData.ProposerSelector.GetProposer(stateData.Height, stateData.Round)
		if err != nil {
			logger.Error("failed to get proposer", "err", err)
			return nil // not a critical error, as we don't propose anyway
		}
		logger.Info("propose step; not our turn to propose",
			"proposer_proTxHash", prop.ProTxHash.ShortString(),
			"node_proTxHash", proTxHash.ShortString(),
			"step", stateData.Step)
		return nil
	}
	// In replay mode, we don't propose blocks.
	if c.replayMode {
		logger.Debug("enter propose step; our turn to propose but in replay mode, not proposing")
		return nil
	}

	logger.Info("propose step; our turn to propose",
		"node_proTxHash", proTxHash.ShortString(),
		"proposer_proTxHash", proTxHash.ShortString(),
		"step", stateData.Step,
	)
	// Flush the WAL. Otherwise, we may not recompute the same proposal to sign,
	// and the privVal will refuse to sign anything.
	if err := c.wal.FlushAndSync(); err != nil {
		c.logger.Error("failed flushing WAL to disk")
	}
	return c.proposalCreator.Create(ctx, height, round, &stateData.RoundState)
}
