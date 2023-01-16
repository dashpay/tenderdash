package consensus

import (
	"context"
	"time"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	"github.com/tendermint/tendermint/libs/log"
	tmtime "github.com/tendermint/tendermint/libs/time"
	"github.com/tendermint/tendermint/types"
)

// Behavior provides some common functionality like a convenient way of command executing and other
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

// EnterNewRound executes enter-new-round command
func (b *Behavior) EnterNewRound(ctx context.Context, stateData *StateData, event EnterNewRoundEvent) error {
	return b.execCommand(ctx, EnterNewRoundType, stateData, event)
}

// EnterPropose executes enter-propose command
func (b *Behavior) EnterPropose(ctx context.Context, stateData *StateData, event EnterProposeEvent) error {
	return b.execCommand(ctx, EnterProposeType, stateData, event)
}

// SetProposal executes set-proposal command
func (b *Behavior) SetProposal(ctx context.Context, stateData *StateData, event SetProposalEvent) error {
	return b.execCommand(ctx, SetProposalType, stateData, event)
}

// TryAddVote executes try-add-vote command
func (b *Behavior) TryAddVote(ctx context.Context, stateData *StateData, event TryAddVoteEvent) error {
	return b.execCommand(ctx, TryAddVoteType, stateData, event)
}

// EnterPrevote executes enter-prevote command
func (b *Behavior) EnterPrevote(ctx context.Context, stateData *StateData, event EnterPrevoteEvent) error {
	return b.execCommand(ctx, EnterPrevoteType, stateData, event)
}

// EnterPrecommit executes enter-precommit command
func (b *Behavior) EnterPrecommit(ctx context.Context, stateData *StateData, event EnterPrecommitEvent) error {
	return b.execCommand(ctx, EnterPrecommitType, stateData, event)
}

// EnterCommit executes enter-commit command
func (b *Behavior) EnterCommit(ctx context.Context, stateData *StateData, event EnterCommitEvent) error {
	return b.execCommand(ctx, EnterCommitType, stateData, event)
}

// TryAddCommit executes try-add-commit command
func (b *Behavior) TryAddCommit(ctx context.Context, stateData *StateData, event TryAddCommitEvent) error {
	return b.execCommand(ctx, TryAddCommitType, stateData, event)
}

// AddCommit executes add-commit command
func (b *Behavior) AddCommit(ctx context.Context, stateData *StateData, event AddCommitEvent) error {
	return b.execCommand(ctx, AddCommitType, stateData, event)
}

// AddProposalBlockPart executes add-proposal-block-part command
func (b *Behavior) AddProposalBlockPart(ctx context.Context, stateData *StateData, event AddProposalBlockPartEvent) error {
	return b.execCommand(ctx, AddProposalBlockPartType, stateData, event)
}

func (b *Behavior) ProposalCompleted(ctx context.Context, stateData *StateData, event ProposalCompletedEvent) error {
	return b.execCommand(ctx, ProposalCompletedType, stateData, event)
}

// TryFinalizeCommit executes try-finalize-commit command
func (b *Behavior) TryFinalizeCommit(ctx context.Context, stateData *StateData, event TryFinalizeCommitEvent) error {
	return b.execCommand(ctx, TryFinalizeCommitType, stateData, event)
}

// ApplyCommit executes apply-commit command
func (b *Behavior) ApplyCommit(ctx context.Context, stateData *StateData, event ApplyCommitEvent) error {
	return b.execCommand(ctx, ApplyCommitType, stateData, event)
}

// EnterPrevoteWait executes enter-prevote-wait command
func (b *Behavior) EnterPrevoteWait(ctx context.Context, stateData *StateData, event EnterPrevoteWaitEvent) error {
	return b.execCommand(ctx, EnterPrevoteWaitType, stateData, event)
}

// EnterPrecommitWait executes enter-precommit-wait command
func (b *Behavior) EnterPrecommitWait(ctx context.Context, stateData *StateData, event EnterPrecommitWaitEvent) error {
	return b.execCommand(ctx, EnterPrecommitWaitType, stateData, event)
}

// DecideProposal executes decide-proposal command
func (b *Behavior) DecideProposal(ctx context.Context, stateData *StateData, event DecideProposalEvent) error {
	return b.execCommand(ctx, DecideProposalType, stateData, event)
}

// DoPrevote executes do-prevote command
func (b *Behavior) DoPrevote(ctx context.Context, stateData *StateData, event DoPrevoteEvent) error {
	return b.execCommand(ctx, DoPrevoteType, stateData, event)
}

// RegisterCommand adds a command handler by event type to the command registry
func (b *Behavior) RegisterCommand(eventType EventType, handler CommandHandler) {
	b.commander.Register(eventType, handler)
}

// GetCommand returns a command handler if a command exists, otherwise nil
func (b *Behavior) GetCommand(eventType EventType) CommandHandler {
	return b.commander.Get(eventType)
}

// ScheduleRound0 enterNewRoundCommand(height, 0) at StartTime
func (b *Behavior) ScheduleRound0(rs cstypes.RoundState) {
	// b.logger.Info("scheduleRound0", "now", tmtime.Now(), "startTime", b.StartTime)
	sleepDuration := rs.StartTime.Sub(tmtime.Now())
	b.ScheduleTimeout(sleepDuration, rs.Height, 0, cstypes.RoundStepNewHeight)
}

// ScheduleTimeout attempts to schedule a timeout (by sending timeoutInfo on the tickChan)
func (b *Behavior) ScheduleTimeout(duration time.Duration, height int64, round int32, step cstypes.RoundStepType) {
	b.timeoutTicker.ScheduleTimeout(timeoutInfo{duration, height, round, step})
}

func (b *Behavior) RecordMetrics(stateData *StateData, height int64, block *types.Block, lastBlockMeta *types.BlockMeta) {
	b.metrics.Validators.Set(float64(stateData.Validators.Size()))
	b.metrics.ValidatorsPower.Set(float64(stateData.Validators.TotalVotingPower()))

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
			if _, val := stateData.Validators.GetByProTxHash(dve.VoteA.ValidatorProTxHash); val != nil {
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

func (b *Behavior) execCommand(ctx context.Context, et EventType, stateData *StateData, event any) error {
	stateEvent := StateEvent{
		EventType: et,
		StateData: stateData,
		Data:      event,
	}
	return b.commander.Execute(ctx, b, stateEvent)
}

func (b *Behavior) updateProposalBlockAndParts(stateData *StateData, blockID types.BlockID) error {
	stateData.replaceProposalBlockOnLockedBlock(blockID)
	if stateData.ProposalBlock.HashesTo(blockID.Hash) || stateData.ProposalBlockParts.HasHeader(blockID.PartSetHeader) {
		return nil
	}
	// If we don't have the block being committed, set up to get it.
	b.logger.Info(
		"commit is for a block we do not know about; set ProposalBlock=nil",
		"proposal", stateData.ProposalBlock.Hash(),
		"commit", blockID.Hash,
	)
	// We're getting the wrong block.
	// Set up ProposalBlockParts and keep waiting.
	stateData.ProposalBlock = nil
	stateData.metrics.MarkBlockGossipStarted()
	stateData.ProposalBlockParts = types.NewPartSetFromHeader(blockID.PartSetHeader)
	err := stateData.Save()
	if err != nil {
		return err
	}
	b.eventPublisher.PublishValidBlockEvent(stateData.RoundState)
	return nil
}
