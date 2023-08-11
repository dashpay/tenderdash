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
		logger.Debug("entering propose step with invalid args", "step", stateData.Step)
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

	if !isProposer {
		logger.Debug("propose step; not our turn to propose",
			"proposer_proTxHash", stateData.Validators.GetProposer().ProTxHash,
			"node_proTxHash", proTxHash.String())
		return nil
	}
	logger.Debug("propose step; our turn to propose",
		"proposer_proTxHash", proTxHash.ShortString(),
	)
	// Flush the WAL. Otherwise, we may not recompute the same proposal to sign,
	// and the privVal will refuse to sign anything.
	if err := c.wal.FlushAndSync(); err != nil {
		c.logger.Error("failed flushing WAL to disk")
	}
	return c.proposalCreator.Create(ctx, height, round, &stateData.RoundState)
}
