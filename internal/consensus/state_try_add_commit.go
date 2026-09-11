package consensus

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	"github.com/dashpay/tenderdash/dash"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

type TryAddCommitEvent struct {
	Commit     *types.Commit
	PeerID     types.NodeID
	FromReplay bool
}

// GetType returns TryAddCommitType event-type
func (e *TryAddCommitEvent) GetType() EventType {
	return TryAddCommitType
}

// TryAddCommitAction ...
// If we received a commit message from an external source try to add it then finalize it.
type TryAddCommitAction struct {
	logger log.Logger
	// create and execute blocks
	eventPublisher *EventPublisher
	blockExec      *blockExecutor
	peerErrorQueue *chanQueue[peerErrorMsg]
	metrics        *Metrics

	verificationBudget types.VerificationBudget
}

// Execute ...
func (cs *TryAddCommitAction) Execute(ctx context.Context, stateEvent StateEvent) error {
	event := stateEvent.Data.(*TryAddCommitEvent)
	stateData := stateEvent.StateData
	commit := event.Commit
	peerID := event.PeerID
	fromReplay := event.FromReplay
	ctx = ctxWithPeerVerificationBudget(ctx, peerID, fromReplay, cs.verificationBudget)

	// Let's only add one remote commit
	if stateData.Commit != nil {
		return nil
	}

	rs := stateData.RoundState

	// We need to first verify that the commit received wasn't for a future round,
	// If it was then we must go to next round
	if commit.Height == rs.Height && commit.Round > rs.Round {
		cs.logger.Trace("commit received for a later round", "height", commit.Height,
			"our_round", rs.Round, "commit_round", commit.Round)
		verified, err := cs.prepareCommitForApply(ctx, stateData, commit, peerID, true)
		if err != nil {
			cs.handleCommitVerifyError(err, peerID, fromReplay)
			return err
		}
		if verified {
			if err := stateEvent.Ctrl.Dispatch(ctx, &EnterNewRoundEvent{Height: stateData.Height, Round: commit.Round}, stateData); err != nil {
				return err
			}
			if stateData.holdsProposalBlock(commit.BlockID) {
				// The retained block needs no further part to trigger application.
				return stateEvent.Ctrl.Dispatch(ctx, &AddCommitEvent{Commit: commit}, stateData)
			}
			return nil
		}
	}

	// First lets verify that the commit is what we are expecting
	verified, err := cs.prepareCommitForApply(ctx, stateData, commit, peerID, false)
	if err != nil {
		cs.handleCommitVerifyError(err, peerID, fromReplay)
		return err
	}
	if !verified {
		if stateData.Commit != nil {
			cs.eventPublisher.PublishValidBlockEvent(stateData.RoundState)
		}
		return nil
	}

	// prepareCommitForApply has already established that the block is held.
	// Restated so that a later change there cannot silently let the round step
	// stand in for it again: a commit parked on the step is parked for good,
	// because a part set completes exactly once.
	if !stateData.holdsProposalBlock(commit.BlockID) {
		cs.logger.Error("commit verified against a block the round state does not hold",
			"height", commit.Height,
			"round", commit.Round,
			"commit_block", commit.BlockID.Hash,
		)
		return nil
	}

	// Below the guard, so that the guard firing leaves nothing behind. Setting
	// Commit is what stops a later commit being reconsidered, and a round holding
	// one it never dispatched waits for an event that will not arrive.
	stateData.Commit = commit
	return stateEvent.Ctrl.Dispatch(ctx, &AddCommitEvent{Commit: commit}, stateData)
}

// handleCommitVerifyError reports the sender for eviction when a commit failed
// verification in a way only a dishonest peer can cause.
//
// Only types.ErrInvalidCommitSignature qualifies: a node stores a commit solely
// after verifying it, so a forged threshold signature cannot originate from an
// honest relayer. Every other failure — a commit for a block we do not have, a
// quorum-hash disagreement, a local finalization fault — is reachable by an
// honest or merely misconfigured peer, and evicting on those would partition the
// network. Replayed messages are exempt entirely: the WAL re-dispatches them
// under the original PeerID, so a peer would otherwise be evicted at restart for
// a message it sent long ago.
func (cs *TryAddCommitAction) handleCommitVerifyError(err error, peerID types.NodeID, fromReplay bool) {
	if peerID != "" && !fromReplay {
		cs.metrics.CommitVerifyFailures.With("reason", commitVerifyFailureReason(err)).Add(1)
		if errors.Is(err, types.ErrVerificationBudgetExhausted) {
			cs.metrics.VerificationBudgetDrops.Add(1)
		}
	}
	if cs.peerErrorQueue == nil || fromReplay {
		return
	}

	if !errors.As(err, &types.ErrInvalidCommitSignature{}) {
		return
	}

	// Never block: this runs on the single consensus goroutine, and the reactor
	// drains the queue. Dropping a report under saturation costs one missed
	// eviction, whereas blocking would stall consensus.
	select {
	case cs.peerErrorQueue.ch <- peerErrorMsg{PeerID: peerID, Err: err, Fatal: true}:
	default:
	}
}

// commitVerifyFailureReason classifies a commit rejection for the metric. The
// classes separate what they say about this node from what they say about the
// sender: a quorum-hash disagreement usually means our validator set is stale,
// a forged signature means the sender is dishonest, and an exhausted budget
// means neither.
func commitVerifyFailureReason(err error) string {
	switch {
	case errors.As(err, &types.ErrInvalidCommitQuorumHash{}):
		return "quorum_hash"
	case errors.As(err, &types.ErrVoteExtensionCountMismatch{}):
		return "extension_count"
	case errors.As(err, &types.ErrInvalidCommitSignature{}):
		return "invalid_signature"
	case errors.Is(err, types.ErrVerificationBudgetExhausted):
		return "budget"
	default:
		return "other"
	}
}

// verifyCommitBlock checks that commit.BlockID describes the block this round
// holds. A BlockID has three fields and each is asked separately, so an operator
// is told which one disagreed; a single combined comparison would report only
// that something did.
//
// A false return with a nil error means the block has not arrived yet, which is
// the ordinary case for a commit that overtook it.
func verifyCommitBlock(
	ctx context.Context,
	logger log.Logger,
	stateData *StateData,
	commit *types.Commit,
) (bool, error) {
	block, blockParts := stateData.ProposalBlock, stateData.ProposalBlockParts
	if block == nil {
		return false, nil
	}
	if !blockParts.HasHeader(commit.BlockID.PartSetHeader) {
		return false, fmt.Errorf("expected ProposalBlockParts header to be commit header")
	}
	proTxHash := dash.MustProTxHashFromContext(ctx)
	if !block.HashesTo(commit.BlockID.Hash) {
		logger.Error("proposal block does not hash to commit hash",
			"height", commit.Height,
			"node_proTxHash", proTxHash.ShortString(),
			"block", block,
			"commit", commit,
			"complete_proposal", stateData.isProposalComplete(),
		)
		return false, fmt.Errorf("cannot finalize commit; proposal block does not hash to commit hash")
	}
	// The third field, and the only one nothing else here would notice. A commit
	// agreeing on hash and part set header while naming a different state ID is
	// applied against this block and then recorded carrying its own BlockID. The
	// next height compares that record against the state this block produced;
	// they disagree, and no proposer at any round can satisfy both.
	if !bytes.Equal(block.StateID().Hash(), commit.BlockID.StateID) {
		logger.Error("commit state ID does not match the block it commits",
			"height", commit.Height,
			"node_proTxHash", proTxHash.ShortString(),
			"block_state_id", block.StateID().Hash(),
			"commit_state_id", commit.BlockID.StateID,
		)
		return false, fmt.Errorf("cannot finalize commit; proposal block state ID does not match commit state ID")
	}
	return true, nil
}

// prepareCommitForApply verifies the commit and, unless ignoreProposalBlock, the
// block it names: it runs that block through the application, so a true return
// means the block is processed and validated, not merely that a signature checked
// out.
func (cs *TryAddCommitAction) prepareCommitForApply(
	ctx context.Context,
	stateData *StateData,
	commit *types.Commit,
	peerID types.NodeID,
	ignoreProposalBlock bool,
) (verified bool, err error) {
	verified, err = stateData.readyToApplyCommit(
		commit,
		peerID,
		ignoreProposalBlock,
		verificationBudgetFromCtx(ctx),
	)
	if !verified || err != nil {
		return verified, err
	}
	if ignoreProposalBlock {
		return true, nil
	}
	if verified, err := verifyCommitBlock(ctx, cs.logger, stateData, commit); !verified || err != nil {
		return verified, err
	}
	// We have a correct block, let's process it before applying the commit
	err = cs.blockExec.ensureProcess(ctx, &stateData.RoundState, commit.Round)
	if err != nil {
		if errors.Is(err, abciclient.ErrClientStopped) {
			// this is a non-recoverable error in current architecture
			panic(fmt.Errorf("ABCI client stopped, Tenderdash needs to be restarted: %w", err))
		}
		return false, fmt.Errorf("unable to process proposal: %w", err)
	}
	err = cs.blockExec.validate(ctx, stateData)
	if err != nil {
		return false, fmt.Errorf("+2/3 committed an invalid block: %w", err)
	}
	return true, nil
}
