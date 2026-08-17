package consensus

import (
	"context"
	"errors"
	"fmt"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
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
		cs.logger.Trace("Commit received for a later round", "height", commit.Height, "our round",
			rs.Round, "commit round", commit.Round)
		verified, err := cs.verifyCommit(ctx, stateData, commit, peerID, true)
		if err != nil {
			cs.handleCommitVerifyError(err, peerID, fromReplay)
			return err
		}
		if verified {
			_ = stateEvent.Ctrl.Dispatch(ctx, &EnterNewRoundEvent{Height: stateData.Height, Round: commit.Round}, stateData)
			// We are now going to receive the block, so initialize the block parts.
			if stateData.ProposalBlockParts == nil {
				stateData.ProposalBlockParts = types.NewPartSetFromHeader(commit.BlockID.PartSetHeader)
			}

			return nil
		}
	}

	// First lets verify that the commit is what we are expecting
	verified, err := cs.verifyCommit(ctx, stateData, commit, peerID, false)
	if err != nil {
		cs.handleCommitVerifyError(err, peerID, fromReplay)
		return err
	}
	if !verified {
		return nil
	}

	stateData.Commit = commit

	// We need to make sure we are past the Propose step
	if stateData.Step <= cstypes.RoundStepPropose {
		// In this case we need to apply the commit after the proposal block comes in
		return nil
	}
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
	if peerID != "" && !fromReplay && errors.Is(err, types.ErrVerificationBudgetExhausted) {
		cs.metrics.VerificationBudgetDrops.Add(1)
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

func (cs *TryAddCommitAction) verifyCommit(ctx context.Context, stateData *StateData, commit *types.Commit, peerID types.NodeID, ignoreProposalBlock bool) (verified bool, err error) {
	verified, err = stateData.verifyCommit(
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
	block, blockParts := stateData.ProposalBlock, stateData.ProposalBlockParts
	if block == nil {
		return false, nil
	}
	if !blockParts.HasHeader(commit.BlockID.PartSetHeader) {
		return false, fmt.Errorf("expected ProposalBlockParts header to be commit header")
	}
	proTxHash := dash.MustProTxHashFromContext(ctx)
	if !block.HashesTo(commit.BlockID.Hash) {
		cs.logger.Error("proposal block does not hash to commit hash",
			"height", commit.Height,
			"node_proTxHash", proTxHash.ShortString(),
			"block", block,
			"commit", commit,
			"complete_proposal", stateData.isProposalComplete(),
		)
		return false, fmt.Errorf("cannot finalize commit; proposal block does not hash to commit hash")
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
