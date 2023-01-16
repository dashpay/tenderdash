package consensus

import (
	"context"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	"github.com/tendermint/tendermint/libs/log"
	tmtime "github.com/tendermint/tendermint/libs/time"
)

type EnterProposeEvent struct {
	Height int64
	Round  int32
}

// EnterProposeCommand ...
// Enter (CreateEmptyBlocks): from enterNewRoundCommand(height,round)
// Enter (CreateEmptyBlocks, CreateEmptyBlocksInterval > 0 ):
// after enterNewRoundCommand(height,round), after timeout of CreateEmptyBlocksInterval
// Enter (!CreateEmptyBlocks) : after enterNewRoundCommand(height,round), once txs are in the mempool
// Caller should hold cs.mtx lock
type EnterProposeCommand struct {
	logger        log.Logger
	privValidator privValidator
	msgInfoQueue  *msgInfoQueue
	wal           WALWriteFlusher
	replayMode    bool
	metrics       *Metrics
	blockExec     *blockExecutor
}

func (cs *EnterProposeCommand) Execute(ctx context.Context, behavior *Behavior, stateEvent StateEvent) error {
	event := stateEvent.Data.(EnterProposeEvent)
	stateData := stateEvent.StateData
	height := event.Height
	round := event.Round

	logger := cs.logger.With("height", height, "round", round)

	if stateData.Height != height || round < stateData.Round || (stateData.Round == round && cstypes.RoundStepPropose <= stateData.Step) {
		logger.Debug("entering propose step with invalid args", "step", stateData.Step)
		return nil
	}

	// If this validator is the proposer of this round, and the previous block time is later than
	// our local clock time, wait to propose until our local clock time has passed the block time.
	if stateData.isProposer(cs.privValidator.ProTxHash) {
		pwt := proposerWaitTime(tmtime.Now(), stateData.state.LastBlockTime)
		if pwt > 0 {
			cs.logger.Debug("enter propose: latest block is newer, sleeping",
				"duration", pwt.String(),
				"last_block_time", stateData.state.LastBlockTime,
				"now", tmtime.Now(),
			)
			behavior.ScheduleTimeout(pwt, height, round, cstypes.RoundStepNewRound)
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
		behavior.newStep(stateData.RoundState)

		// If we have the whole proposal + POL, then goto Prevote now.
		// else, we'll enterPrevote when the rest of the proposal is received (in AddProposalBlockPart),
		// or else after timeoutPropose
		if stateData.isProposalComplete() {
			_ = behavior.EnterPrevote(ctx, stateData, EnterPrevoteEvent{Height: height, Round: round})
		}
	}()

	// If we don't get the proposal and all block parts quick enough, enterPrevote
	behavior.ScheduleTimeout(stateData.proposeTimeout(round), height, round, cstypes.RoundStepPropose)

	// Nothing more to do if we're not a validator
	if cs.privValidator.IsZero() {
		logger.Debug("propose step; not proposing since node is not a validator")
		return nil
	}

	proTxHash := cs.privValidator.ProTxHash

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
			"privValidator", cs.privValidator,
		)
		_ = behavior.DecideProposal(ctx, stateData, DecideProposalEvent{Height: height, Round: round})
	} else {
		logger.Debug("propose step; not our turn to propose",
			"proposer",
			stateData.Validators.GetProposer().ProTxHash,
			"privValidator",
			cs.privValidator)
	}
	return nil
}

func (cs *EnterProposeCommand) Subscribe(observer *Observer) {
	observer.Subscribe(SetMetrics, func(a any) error {
		cs.metrics = a.(*Metrics)
		return nil
	})
	observer.Subscribe(SetReplayMode, func(a any) error {
		cs.replayMode = a.(bool)
		return nil
	})
}
