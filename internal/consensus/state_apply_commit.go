package consensus

import (
	"context"
	"errors"
	"fmt"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

type ApplyCommitEvent struct {
	Commit *types.Commit
	// FromOwnPrecommits marks a commit this node assembled from its own +2/3
	// precommits, which there is no point assembling again if it is rejected.
	FromOwnPrecommits bool
	// Rejected reports that the application rejected the commit extensions and
	// consensus recovered without treating the caller as a faulty peer.
	Rejected bool
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
	candidates     *commitCandidates
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

	// Parked commits and locally assembled commits bypass TryAddCommit's block check.
	if commit != nil {
		ready, err := verifyCommitBlock(ctx, c.logger, stateData, commit)
		if err != nil {
			return err
		}
		if !ready {
			return errors.New("cannot apply commit without its proposal block")
		}
	}

	c.blockExec.mustEnsureProcess(ctx, &stateData.RoundState, round)
	c.blockExec.mustValidate(ctx, stateData)

	if commit != nil {
		// The application must accept the commit's extension vector before the
		// block and commit are persisted or finalized. Until it does, the commit
		// is not published in stateData.Commit, unless adoptCommit parked it there
		// while its block was missing; TryAddCommit's parked-commit guard keeps
		// other commits out meanwhile.
		if err := sm.VerifyCommitExtensions(ctx, c.blockExec.blockExec, commit); err != nil {
			event.Rejected = true
			c.metrics.CommitVerifyFailures.With("reason", commitVerifyFailureReason(err)).Add(1)
			c.logger.Debug("application rejected commit vote extensions",
				"height", commit.Height, "round", commit.Round, "error", err)
			if recoveryErr := errors.Join(
				c.discardRejectedCommit(stateData),
				c.recoverFromRejectedCommit(ctx, stateEvent.Ctrl, stateData, event.FromOwnPrecommits),
			); recoveryErr != nil {
				return recoveryErr
			}
			return nil
		}
		stateData.Commit = commit
		if stateData.enterApplyCommit(commit.Round) {
			c.eventPublisher.PublishNewRoundStepEvent(stateData.RoundState)
		}
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
	stateCopy, finalizeResp, err := c.blockExec.finalize(ctx, stateData, commit)
	if err != nil {
		c.logger.Error("failed to apply block", "err", err)
		// If something went wrong within ABCI client, it can stop and we can't recover from it.
		// So, we panic here to ensure that the node will be restarted.
		panic(fmt.Errorf("failed to finalize block %X at height %d: %w", block.Hash(), block.Height, err))
	}

	lastBlockMeta := c.blockStore.LoadBlockMeta(height - 1)

	// must be called before we update state
	c.RecordMetrics(stateData, height, block, lastBlockMeta)

	// NewHeightStep!
	stateData.updateToState(stateCopy, commit, c.blockStore)

	// The application may ask us not to wait for transactions before proposing
	// the next height (ResponseFinalizeBlock.propose_next_block_immediately).
	// updateToState cleared the previous hint, so it only ever applies to the
	// height that follows the block just finalized.
	if finalizeResp.GetProposeNextBlockImmediately() {
		c.logger.Debug("application requested the next block without waiting for transactions",
			"height", stateData.Height)
		stateData.ProposeNextBlockImmediately = true
	}

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

// discardRejectedCommit keeps the processed block but forgets the commit, so a
// replacement for the same block can be applied without processing it again.
func (c *ApplyCommitAction) discardRejectedCommit(stateData *StateData) error {
	stateData.discardCommit()
	if stateData.Step == cstypes.RoundStepApplyCommit {
		// Only EnterCommit, on +2/3 precommits for the block, reaches this step
		// before a commit is accepted, so the round had at least reached Precommit.
		stateData.updateRoundStep(stateData.Round, cstypes.RoundStepPrecommit)
	}
	return stateData.Save()
}

// recoverFromRejectedCommit finds another way to finish the height after a
// commit's extensions were rejected. In order, it tries:
//
//  1. the commits peers sent while the rejected one was parked, which they will
//     not send again (see commitCandidates);
//  2. this node's own +2/3 precommits for the block, if the rejected commit did
//     not come from them;
//  3. the next round: a precommit-wait timeout, whose handler moves the round
//     on even from a step whose own timeouts have already fired. The new round
//     clears every peer's record of having sent us a commit, so peers that
//     have one gossip it again.
//
// Replacements go through the same verification as a commit received from the
// network. Any of them that is rejected lands back here while the loop is
// running and returns at once, so recovery is iterative. Nothing is applied
// twice: success moves stateData to the next height, which ends the loop and
// makes the remaining candidates stale.
func (c *ApplyCommitAction) recoverFromRejectedCommit(
	ctx context.Context,
	ctrl *Controller,
	stateData *StateData,
	fromOwnPrecommits bool,
) error {
	if c.candidates.recovering {
		return nil
	}
	c.candidates.recovering = true
	defer func() { c.candidates.recovering = false }()

	height := stateData.Height
	pending := func() bool { return stateData.Height == height && stateData.Commit == nil }

	for pending() {
		candidate, ok := c.candidates.pop(height)
		if !ok {
			break
		}
		candidateCtx := msgInfoWithCtx(ctx, msgInfo{Msg: &CommitMessage{candidate.commit}, PeerID: candidate.peerID})
		err := ctrl.Dispatch(candidateCtx, &TryAddCommitEvent{
			Commit:     candidate.commit,
			PeerID:     candidate.peerID,
			FromReplay: candidate.fromReplay,
		}, stateData)
		if err != nil {
			c.logger.Debug("commit received while another was parked cannot replace it",
				"height", height, "peer", candidate.peerID, "error", err)
		}
	}

	if pending() && !fromOwnPrecommits {
		blockID, ok := stateData.Votes.Precommits(stateData.Round).TwoThirdsMajority()
		if ok && stateData.holdsProposalBlock(blockID) {
			err := ctrl.Dispatch(ctx, &EnterCommitEvent{Height: height, CommitRound: stateData.Round}, stateData)
			if err != nil {
				c.logger.Error("cannot commit from own precommits", "height", height, "error", err)
			}
		}
	}

	if pending() {
		c.logger.Debug("no replacement for the rejected commit; moving to the next round",
			"height", height, "round", stateData.Round)
		return ctrl.Dispatch(ctx, &EnterNewRoundEvent{Height: height, Round: stateData.Round + 1}, stateData)
	}
	return nil
}

func (c *ApplyCommitAction) RecordMetrics(stateData *StateData, height int64, block *types.Block, lastBlockMeta *types.BlockMeta) {
	totalValidators := stateData.Validators.Size()
	totalValidatorsPower := stateData.Validators.TotalVotingPower()

	c.metrics.Validators.Set(float64(totalValidators))
	c.metrics.ValidatorsPower.Set(float64(totalValidatorsPower))

	// Calculate validators that didn't sign

	// We initialize with total validators count and power, and then decrement as we find the validators
	// who have signed the precommit
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
