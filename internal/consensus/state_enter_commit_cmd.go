package consensus

import (
	"context"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	tmevents "github.com/tendermint/tendermint/libs/events"
	"github.com/tendermint/tendermint/libs/log"
	tmtime "github.com/tendermint/tendermint/libs/time"
)

type EnterCommitEvent struct {
	Height      int64
	CommitRound int32
}

// EnterCommitCommand ...
// Enter: +2/3 precommits for block
type EnterCommitCommand struct {
	logger         log.Logger
	eventPublisher *EventPublisher
	metrics        *Metrics
	evsw           tmevents.EventSwitch
}

// Execute ...
func (cs *EnterCommitCommand) Execute(ctx context.Context, behavior *Behavior, stateEvent StateEvent) error {
	event := stateEvent.Data.(EnterCommitEvent)
	stateData := stateEvent.StateData
	height := event.Height
	commitRound := event.CommitRound
	logger := cs.logger.With("new_height", height, "commit_round", commitRound)

	if stateData.Height != height || cstypes.RoundStepApplyCommit <= stateData.Step {
		logger.Debug("entering commit step with invalid args",
			"height", stateData.Height,
			"round", stateData.Round,
			"step", stateData.Step)
		return nil
	}

	logger.Debug("entering commit step",
		"height", stateData.Height,
		"round", stateData.Round,
		"step", stateData.Step)

	defer func() {
		// Done enterCommit:
		// keep cs.Round the same, commitRound points to the right Precommits set.
		stateData.updateRoundStep(stateData.Round, cstypes.RoundStepApplyCommit)
		stateData.CommitRound = commitRound
		stateData.CommitTime = tmtime.Now()
		behavior.newStep(stateData.RoundState)

		// Maybe finalize immediately.
		_ = behavior.TryFinalizeCommit(ctx, stateData, TryFinalizeCommitEvent{Height: height})
	}()

	blockID, ok := stateData.Votes.Precommits(commitRound).TwoThirdsMajority()
	if !ok {
		panic("RunActionCommit() expects +2/3 precommits")
	}

	return behavior.updateProposalBlockAndParts(stateData, blockID)
}

func (cs *EnterCommitCommand) Subscribe(observer *Observer) {
	observer.Subscribe(SetMetrics, func(a any) error {
		cs.metrics = a.(*Metrics)
		return nil
	})
}
