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

func (cs *EnterProposeCommand) Execute(ctx context.Context, behaviour *Behaviour, stateEvent StateEvent) (any, error) {
	event := stateEvent.Data.(EnterProposeEvent)
	appState := stateEvent.AppState
	height := event.Height
	round := event.Round

	logger := cs.logger.With("height", height, "round", round)

	if appState.Height != height || round < appState.Round || (appState.Round == round && cstypes.RoundStepPropose <= appState.Step) {
		logger.Debug("entering propose step with invalid args", "step", appState.Step)
		return nil, nil
	}

	// If this validator is the proposer of this round, and the previous block time is later than
	// our local clock time, wait to propose until our local clock time has passed the block time.
	if appState.isProposer(cs.privValidator.ProTxHash) {
		pwt := proposerWaitTime(tmtime.Now(), appState.state.LastBlockTime)
		if pwt > 0 {
			cs.logger.Debug("enter propose: latest block is newer, sleeping",
				"duration", pwt.String(),
				"last_block_time", appState.state.LastBlockTime,
				"now", tmtime.Now(),
			)
			behaviour.ScheduleTimeout(pwt, height, round, cstypes.RoundStepNewRound)
			return nil, nil
		}
	}

	logger.Debug("entering propose step",
		"height", appState.Height,
		"round", appState.Round,
		"step", appState.Step)

	defer func() {
		// Done enterPropose:
		appState.updateRoundStep(round, cstypes.RoundStepPropose)
		behaviour.newStep(appState.RoundState)

		// If we have the whole proposal + POL, then goto Prevote now.
		// else, we'll enterPrevote when the rest of the proposal is received (in AddProposalBlockPart),
		// or else after timeoutPropose
		if appState.isProposalComplete() {
			_ = behaviour.EnterPrevote(ctx, appState, EnterPrevoteEvent{Height: height, Round: round})
		}
	}()

	// If we don't get the proposal and all block parts quick enough, enterPrevote
	behaviour.ScheduleTimeout(appState.proposeTimeout(round), height, round, cstypes.RoundStepPropose)

	// Nothing more to do if we're not a validator
	if cs.privValidator.IsZero() {
		logger.Debug("propose step; not proposing since node is not a validator")
		return nil, nil
	}

	proTxHash := cs.privValidator.ProTxHash

	// if not a validator, we're done
	if !appState.Validators.HasProTxHash(proTxHash) {
		logger.Debug("propose step; not proposing since node is not in the validator set",
			"proTxHash", proTxHash.ShortString(),
			"vals", appState.Validators)
		return nil, nil
	}

	if appState.isProposer(proTxHash) {
		logger.Debug("propose step; our turn to propose",
			"proposer", proTxHash.ShortString(),
			"privValidator", cs.privValidator,
		)
		_ = behaviour.DecideProposal(ctx, appState, DecideProposalEvent{Height: height, Round: round})
	} else {
		logger.Debug("propose step; not our turn to propose",
			"proposer",
			appState.Validators.GetProposer().ProTxHash,
			"privValidator",
			cs.privValidator)
	}
	return nil, nil
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
