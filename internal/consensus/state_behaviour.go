package consensus

import (
	"context"
	"time"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	"github.com/tendermint/tendermint/libs/log"
	tmtime "github.com/tendermint/tendermint/libs/time"
	"github.com/tendermint/tendermint/types"
)

type Behavior struct {
	wal            WALWriter
	eventPublisher *EventPublisher
	commander      *CommandExecutor
	logger         log.Logger

	// state changes may be triggered by: msgs from peers,
	// msgs from ourself, or by timeouts
	timeoutTicker TimeoutTicker

	metrics *Metrics

	nSteps int
}

func (b *Behavior) EnterNewRound(ctx context.Context, appState *AppState, event EnterNewRoundEvent) error {
	_, err := b.execCommand(ctx, EnterNewRoundType, appState, event)
	return err
}

func (b *Behavior) EnterPropose(ctx context.Context, appState *AppState, event EnterProposeEvent) error {
	_, err := b.execCommand(ctx, EnterProposeType, appState, event)
	return err
}

func (b *Behavior) SetProposal(ctx context.Context, appState *AppState, event SetProposalEvent) error {
	_, err := b.execCommand(ctx, SetProposalType, appState, event)
	return err
}

func (b *Behavior) TryAddVote(ctx context.Context, appState *AppState, event TryAddVoteEvent) (bool, error) {
	res, err := b.execCommand(ctx, TryAddVoteType, appState, event)
	return res.(bool), err
}

func (b *Behavior) EnterPrevote(ctx context.Context, appState *AppState, event EnterPrevoteEvent) error {
	_, err := b.execCommand(ctx, EnterPrevoteType, appState, event)
	return err
}

func (b *Behavior) EnterPrecommit(ctx context.Context, appState *AppState, event EnterPrecommitEvent) error {
	_, err := b.execCommand(ctx, EnterPrecommitType, appState, event)
	return err
}

func (b *Behavior) EnterCommit(ctx context.Context, appState *AppState, event EnterCommitEvent) error {
	_, err := b.execCommand(ctx, EnterCommitType, appState, event)
	return err
}

func (b *Behavior) TryAddCommit(ctx context.Context, appState *AppState, event TryAddCommitEvent) (bool, error) {
	res, err := b.execCommand(ctx, TryAddCommitType, appState, event)
	return res.(bool), err
}

func (b *Behavior) AddCommit(ctx context.Context, appState *AppState, event AddCommitEvent) (bool, error) {
	res, err := b.execCommand(ctx, AddCommitType, appState, event)
	return res.(bool), err
}

func (b *Behavior) AddProposalBlockPart(ctx context.Context, appState *AppState, event AddProposalBlockPartEvent) (bool, error) {
	res, err := b.execCommand(ctx, AddProposalBlockPartType, appState, event)
	return res.(bool), err
}

func (b *Behavior) TryFinalizeCommit(ctx context.Context, appState *AppState, event TryFinalizeCommitEvent) {
	_, _ = b.execCommand(ctx, TryFinalizeCommitType, appState, event)
}

func (b *Behavior) ApplyCommit(ctx context.Context, appState *AppState, event ApplyCommitEvent) {
	_, _ = b.execCommand(ctx, ApplyCommitType, appState, event)
}

func (b *Behavior) EnterPrevoteWait(ctx context.Context, appState *AppState, event EnterPrevoteWaitEvent) {
	_, _ = b.execCommand(ctx, EnterPrevoteWaitType, appState, event)
}

func (b *Behavior) EnterPrecommitWait(ctx context.Context, appState *AppState, event EnterPrecommitWaitEvent) {
	_, _ = b.execCommand(ctx, EnterPrecommitWaitType, appState, event)
}

func (b *Behavior) DecideProposal(ctx context.Context, appState *AppState, event DecideProposalEvent) error {
	_, err := b.execCommand(ctx, DecideProposalType, appState, event)
	return err
}

func (b *Behavior) DoPrevote(ctx context.Context, appState *AppState, event DoPrevoteEvent) error {
	_, err := b.execCommand(ctx, DoPrevoteType, appState, event)
	return err
}

func (b *Behavior) RegisterCommand(eventType EventType, handler CommandHandler) {
	b.commander.Register(eventType, handler)
}

func (b *Behavior) execCommand(ctx context.Context, et EventType, appState *AppState, event any) (any, error) {
	stateEvent := StateEvent{
		EventType: et,
		AppState:  appState,
		Data:      event,
	}
	return b.commander.Execute(ctx, b, stateEvent)
}

func (b *Behavior) updateProposalBlockAndParts(appState *AppState, blockID types.BlockID) error {
	appState.replaceProposalBlockOnLockedBlock(blockID)
	if appState.ProposalBlock.HashesTo(blockID.Hash) || appState.ProposalBlockParts.HasHeader(blockID.PartSetHeader) {
		return nil
	}
	// If we don't have the block being committed, set up to get it.
	b.logger.Info(
		"commit is for a block we do not know about; set ProposalBlock=nil",
		"proposal", appState.ProposalBlock.Hash(),
		"commit", blockID.Hash,
	)
	// We're getting the wrong block.
	// Set up ProposalBlockParts and keep waiting.
	appState.ProposalBlock = nil
	appState.metrics.MarkBlockGossipStarted()
	appState.ProposalBlockParts = types.NewPartSetFromHeader(blockID.PartSetHeader)
	err := appState.Save()
	if err != nil {
		return err
	}
	b.eventPublisher.PublishValidBlockEvent(appState.RoundState)
	return nil
}

// ScheduleRound0 enterNewRoundCommand(height, 0) at cs.StartTime
func (b *Behavior) ScheduleRound0(rs cstypes.RoundState) {
	// b.logger.Info("scheduleRound0", "now", tmtime.Now(), "startTime", b.StartTime)
	sleepDuration := rs.StartTime.Sub(tmtime.Now())
	b.ScheduleTimeout(sleepDuration, rs.Height, 0, cstypes.RoundStepNewHeight)
}

// ScheduleTimeout attempts to schedule a timeout (by sending timeoutInfo on the tickChan)
func (b *Behavior) ScheduleTimeout(duration time.Duration, height int64, round int32, step cstypes.RoundStepType) {
	b.timeoutTicker.ScheduleTimeout(timeoutInfo{duration, height, round, step})
}

func (b *Behavior) RecordMetrics(appState *AppState, height int64, block *types.Block, lastBlockMeta *types.BlockMeta) {
	b.metrics.Validators.Set(float64(appState.Validators.Size()))
	b.metrics.ValidatorsPower.Set(float64(appState.Validators.TotalVotingPower()))

	var (
		missingValidators      int
		missingValidatorsPower int64
	)
	b.metrics.MissingValidators.Set(float64(missingValidators))
	b.metrics.MissingValidatorsPower.Set(float64(missingValidatorsPower))

	// NOTE: byzantine validators power and count is only for consensus evidence i.e. duplicate vote
	var (
		byzantineValidatorsPower int64
		byzantineValidatorsCount int64
	)

	for _, ev := range block.Evidence {
		if dve, ok := ev.(*types.DuplicateVoteEvidence); ok {
			if _, val := appState.Validators.GetByProTxHash(dve.VoteA.ValidatorProTxHash); val != nil {
				byzantineValidatorsCount++
				byzantineValidatorsPower += val.VotingPower
			}
		}
	}
	b.metrics.ByzantineValidators.Set(float64(byzantineValidatorsCount))
	b.metrics.ByzantineValidatorsPower.Set(float64(byzantineValidatorsPower))

	if height > 1 && lastBlockMeta != nil {
		b.metrics.BlockIntervalSeconds.Observe(
			block.Time.Sub(lastBlockMeta.Header.Time).Seconds(),
		)
	}

	b.metrics.NumTxs.Set(float64(len(block.Data.Txs)))
	b.metrics.TotalTxs.Add(float64(len(block.Data.Txs)))
	b.metrics.BlockSizeBytes.Observe(float64(block.Size()))
	b.metrics.CommittedHeight.Set(float64(block.Height))
}

func (b *Behavior) newStep(rs cstypes.RoundState) {
	event := rs.RoundStateEvent()
	if err := b.wal.Write(event); err != nil {
		b.logger.Error("failed writing to WAL", "err", err)
	}

	b.nSteps++

	// newStep is called by updateToState in NewState before the eventBus is set!
	b.eventPublisher.PublishNewRoundStepEvent(rs)
}
