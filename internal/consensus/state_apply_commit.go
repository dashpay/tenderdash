package consensus

import (
	"context"
	"fmt"

	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

type ApplyCommitEvent struct {
	Commit *types.Commit
}

// GetType returns ApplyCommitType event-type
func (e *ApplyCommitEvent) GetType() EventType {
	return ApplyCommitType
}

type ApplyCommitAction struct {
	logger log.Logger
	// store blocks and commits
	blockStore sm.BlockStore
	// create and execute blocks
	blockExec      *blockExecutor
	wal            WALWriteFlusher
	scheduler      *roundScheduler
	metrics        *Metrics
	eventPublisher *EventPublisher
}

func (c *ApplyCommitAction) Execute(ctx context.Context, stateEvent StateEvent) error {
	event := stateEvent.Data.(*ApplyCommitEvent)
	stateData := stateEvent.StateData
	commit := event.Commit

	height := stateData.Height
	round := stateData.Round

	if commit != nil {
		height = commit.Height
		round = commit.Round
	}
	c.logger.Info("applying commit", "commit", commit, "height", height, "round", round)

	block, blockParts := stateData.ProposalBlock, stateData.ProposalBlockParts

	c.blockExec.mustEnsureProcess(ctx, &stateData.RoundState, round)
	c.blockExec.mustValidate(ctx, stateData)

	// Save to blockStore
	if commit != nil {
		c.blockStore.SaveBlock(block, blockParts, commit)
	}

	// Write EndHeightMessage{} for this height, implying that the blockstore
	// has saved the block.
	//
	// If we crash before writing this EndHeightMessage{}, we will recover by
	// running ApplyBlock during the ABCI handshake when we restart.  If we
	// didn't save the block to the blockstore before writing
	// EndHeightMessage{}, we'd have to change WAL replay -- currently it
	// complains about replaying for heights where an #ENDHEIGHT entry already
	// exists.
	//
	// Either way, the State should not be resumed until we
	// successfully call ApplyBlock (ie. later here, or in Handshake after
	// restart).
	endMsg := EndHeightMessage{height}
	if err := c.wal.WriteSync(endMsg); err != nil { // NOTE: fsync
		panic(fmt.Errorf(
			"failed to write %v msg to consensus WAL due to %w; check your file system and restart the node",
			endMsg, err,
		))
	}

	// Create a copy of the state for staging and an event cache for txs.
	stateCopy, err := c.blockExec.finalize(ctx, stateData, commit)
	if err != nil {
		c.logger.Error("failed to apply block", "err", err)
		return nil
	}

	lastBlockMeta := c.blockStore.LoadBlockMeta(height - 1)

	// must be called before we update state
	c.RecordMetrics(stateData, height, block, lastBlockMeta)

	// NewHeightStep!
	stateData.updateToState(stateCopy, commit, c.blockStore)
	err = stateData.Save()
	if err != nil {
		return err
	}

	c.eventPublisher.PublishNewRoundStepEvent(stateData.RoundState)

	// c.StartTime is already set.
	// Schedule Round0 to start soon.
	c.scheduler.ScheduleRound0(stateData.RoundState)

	// By here,
	// * c.Height has been increment to height+1
	// * c.Step is now cstypes.RoundStepNewHeight
	// * c.StartTime is set to when we will start round0.
	return nil
}

func (c *ApplyCommitAction) RecordMetrics(stateData *StateData, height int64, block *types.Block, lastBlockMeta *types.BlockMeta) {
	totalValidators := stateData.Validators.Size()
	totalValidatorsPower := stateData.Validators.TotalVotingPower()

	c.metrics.Validators.Set(float64(totalValidators))
	c.metrics.ValidatorsPower.Set(float64(totalValidatorsPower))

	// Calculate validators that didn't sign

	// we initialize with total validators power and count and then decrement as we find the validators
	// who have not signed the precommit
	missingValidators := totalValidators
	missingValidatorsPower := totalValidatorsPower
	precommits := stateData.Votes.Precommits(stateData.CommitRound)

	for _, vote := range precommits.List() {
		if val := stateData.Validators.GetByIndex(vote.ValidatorIndex); val != nil {
			missingValidators--
			missingValidatorsPower -= val.VotingPower
		} else {
			c.logger.Error("precommit received from invalid validator",
				"val", val,
				"vote", vote,
				"height", vote.Height,
				"round", vote.Round)
		}
	}

	c.metrics.MissingValidators.Set(float64(missingValidators))
	c.metrics.MissingValidatorsPower.Set(float64(missingValidatorsPower))

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
	c.metrics.ByzantineValidators.Set(float64(byzantineValidatorsCount))
	c.metrics.ByzantineValidatorsPower.Set(float64(byzantineValidatorsPower))

	if height > 1 && lastBlockMeta != nil {
		c.metrics.BlockIntervalSeconds.Observe(
			block.Time.Sub(lastBlockMeta.Header.Time).Seconds(),
		)
	}

	c.metrics.NumTxs.Set(float64(len(block.Data.Txs)))
	c.metrics.TotalTxs.Add(float64(len(block.Data.Txs)))
	c.metrics.BlockSizeBytes.Observe(float64(block.Size()))
	c.metrics.CommittedHeight.Set(float64(block.Height))
}
