package consensus

import (
	"context"
	"errors"
	"fmt"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	tmstrings "github.com/tendermint/tendermint/internal/libs/strings"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/libs/log"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
)

type TryAddVoteEvent struct {
	Vote   *types.Vote
	PeerID types.NodeID
}

// TryAddVoteCommand ...
// Attempt to add the vote. if its a duplicate signature, dupeout the validator
type TryAddVoteCommand struct {
	// add evidence to the pool
	// when it's detected
	evpool         evidencePool
	logger         log.Logger
	privValidator  privValidator
	eventPublisher *EventPublisher
	blockExec      *sm.BlockExecutor
	metrics        *Metrics
}

// Execute ...
func (cs *TryAddVoteCommand) Execute(ctx context.Context, behavior *Behavior, stateEvent StateEvent) (any, error) {
	stateData := stateEvent.StateData
	event := stateEvent.Data.(TryAddVoteEvent)
	vote, peerID := event.Vote, event.PeerID
	added, err := cs.addVote(ctx, behavior, stateData, vote, peerID)
	if err != nil {
		// If the vote height is off, we'll just ignore it,
		// But if it's a conflicting sig, add it to the cs.evpool.
		// If it's otherwise invalid, punish peer.
		if voteErr, ok := err.(*types.ErrVoteConflictingVotes); ok {
			if cs.privValidator.IsZero() {
				return false, ErrPrivValidatorNotSet
			}

			if cs.privValidator.IsProTxHashEqual(vote.ValidatorProTxHash) {
				cs.logger.Error(
					"found conflicting vote from ourselves; did you unsafe_reset a validator?",
					"height", vote.Height,
					"round", vote.Round,
					"type", vote.Type)

				return added, err
			}

			// report conflicting votes to the evidence pool
			cs.evpool.ReportConflictingVotes(voteErr.VoteA, voteErr.VoteB)
			cs.logger.Debug("found and sent conflicting votes to the evidence pool",
				"vote_a", voteErr.VoteA,
				"vote_b", voteErr.VoteB)

			return added, err
		} else if errors.Is(err, types.ErrVoteNonDeterministicSignature) {
			cs.logger.Debug("vote has non-deterministic signature", "err", err)
		} else {
			// Either
			// 1) bad peer OR
			// 2) not a bad peer? this can also err sometimes with "Unexpected step" OR
			// 3) tmkms use with multiple validators connecting to a single tmkms instance
			//		(https://github.com/tendermint/tendermint/issues/3839).
			cs.logger.Info("failed attempting to add vote", "quorum_hash", stateData.Validators.QuorumHash, "err", err)
			return added, ErrAddingVote
		}
	}

	return added, nil
}

func (cs *TryAddVoteCommand) addVote(
	ctx context.Context,
	behavior *Behavior,
	stateData *StateData,
	vote *types.Vote,
	peerID types.NodeID,
) (added bool, err error) {
	cs.logger.Debug(
		"adding vote",
		"vote", vote,
		"height", stateData.Height,
		"round", stateData.Round,
	)

	// A precommit for the previous height?
	// These come in while we wait timeoutCommit
	if vote.Height+1 == stateData.Height && vote.Type == tmproto.PrecommitType {
		if stateData.Step != cstypes.RoundStepNewHeight {
			// Late precommit at prior height is ignored
			cs.logger.Debug("precommit vote came in after commit timeout and has been ignored", "vote", vote)
			return
		}
		if stateData.LastPrecommits == nil {
			cs.logger.Debug("no last round precommits on node", "vote", vote)
			return
		}

		added, err = stateData.LastPrecommits.AddVote(vote)
		if !added {
			cs.logger.Debug(
				"vote not added",
				"height", vote.Height,
				"vote_type", vote.Type,
				"val_index", vote.ValidatorIndex,
				"cs_height", stateData.Height,
				"error", err,
			)
			return
		}

		cs.logger.Debug("added vote to last precommits", "last_precommits", stateData.LastPrecommits.StringShort())

		err = cs.eventPublisher.PublishVoteEvent(vote)
		if err != nil {
			return added, err
		}

		// if we can skip timeoutCommit and have all the votes now,
		if stateData.bypassCommitTimeout() && stateData.LastPrecommits.HasAll() {
			// go straight to new round (skip timeout commit)
			// cs.scheduleTimeout(time.Duration(0), cs.Height, 0, cstypes.RoundStepNewHeight)
			_ = behavior.EnterNewRound(ctx, stateData, EnterNewRoundEvent{Height: stateData.Height})
		}

		return
	}

	// Height mismatch is ignored.
	// Not necessarily a bad peer, but not favorable behavior.
	if vote.Height != stateData.Height {
		added = false
		cs.logger.Debug("vote ignored and not added", "vote_height", vote.Height, "cs_height", stateData.Height, "peer", peerID)
		return
	}

	// Verify VoteExtension if precommit and not nil
	// https://github.com/tendermint/tendermint/issues/8487
	if vote.Type == tmproto.PrecommitType && !vote.BlockID.IsNil() &&
		!cs.privValidator.IsProTxHashEqual(vote.ValidatorProTxHash) { // Skip the VerifyVoteExtension call if the vote was issued by this validator.

		// The core fields of the vote message were already validated in the
		// consensus reactor when the vote was received.
		// Here, we verify the signature of the vote extension included in the vote
		// message.
		val := stateData.state.Validators.GetByIndex(vote.ValidatorIndex)
		qt, qh := stateData.state.Validators.QuorumType, stateData.state.Validators.QuorumHash
		if err := vote.VerifyExtensionSign(stateData.state.ChainID, val.PubKey, qt, qh); err != nil {
			return false, err
		}

		err := cs.blockExec.VerifyVoteExtension(ctx, vote)
		cs.metrics.MarkVoteExtensionReceived(err == nil)
		if err != nil {
			return false, err
		}
	}

	// Ignore vote if we do not have public keys to verify votes
	if !stateData.Validators.HasPublicKeys {
		added = false
		cs.logger.Debug("vote received on non-validator, ignoring it",
			"vote", vote,
			"cs_height", stateData.Height,
			"peer", peerID,
		)
		return
	}

	cs.logger.Debug(
		"adding vote to vote set",
		"height", stateData.Height,
		"round", stateData.Round,
		"vote", vote,
	)

	height := stateData.Height
	added, err = stateData.Votes.AddVote(vote, peerID)
	if !added {
		if err != nil {
			cs.logger.Error(
				"error adding vote",
				"vote", vote,
				"cs_height", stateData.Height,
				"error", err,
			)
		}
		// Either duplicate, or error upon cs.Votes.AddByIndex()
		return
	}
	if vote.Round == stateData.Round {
		vals := stateData.state.Validators
		val := vals.GetByIndex(vote.ValidatorIndex)
		cs.metrics.MarkVoteReceived(vote.Type, val.VotingPower, vals.TotalVotingPower())
	}

	// TODO discuss about checking error result
	err = cs.eventPublisher.PublishVoteEvent(vote)
	if err != nil {
		return added, err
	}

	switch vote.Type {
	case tmproto.PrevoteType:
		prevotes := stateData.Votes.Prevotes(vote.Round)
		cs.logger.Debug("added vote to prevote", "vote", vote, "prevotes", prevotes.StringShort())

		// Check to see if >2/3 of the voting power on the network voted for any non-nil block.
		if blockID, ok := prevotes.TwoThirdsMajority(); ok && !blockID.IsNil() {
			// Greater than 2/3 of the voting power on the network voted for some
			// non-nil block

			// Update Valid* if we can.
			if stateData.ValidRound < vote.Round && vote.Round == stateData.Round {
				if stateData.ProposalBlock.HashesTo(blockID.Hash) {
					cs.logger.Debug("updating valid block because of POL", "valid_round", stateData.ValidRound, "pol_round", vote.Round)
					stateData.updateValidBlock()
				} else {
					cs.logger.Debug("valid block we do not know about; set ProposalBlock=nil",
						"proposal", tmstrings.LazyBlockHash(stateData.ProposalBlock),
						"block_id", blockID.Hash)

					// we're getting the wrong block
					stateData.ProposalBlock = nil
				}

				if !stateData.ProposalBlockParts.HasHeader(blockID.PartSetHeader) {
					cs.metrics.MarkBlockGossipStarted()
					stateData.ProposalBlockParts = types.NewPartSetFromHeader(blockID.PartSetHeader)
				}
				err = stateData.Save()
				if err != nil {
					return false, err
				}
				cs.eventPublisher.PublishValidBlockEvent(stateData.RoundState)
			}
		}

		// If +2/3 prevotes for *anything* for future round:
		switch {
		case stateData.Round < vote.Round && prevotes.HasTwoThirdsAny():
			// Round-skip if there is any 2/3+ of votes ahead of us
			_ = behavior.EnterNewRound(ctx, stateData, EnterNewRoundEvent{Height: height, Round: vote.Round})

		case stateData.Round == vote.Round && cstypes.RoundStepPrevote <= stateData.Step: // current round
			blockID, ok := prevotes.TwoThirdsMajority()
			if ok && (stateData.isProposalComplete() || blockID.IsNil()) {
				_ = behavior.EnterPrecommit(ctx, stateData, EnterPrecommitEvent{Height: height, Round: vote.Round})
			} else if prevotes.HasTwoThirdsAny() {
				behavior.EnterPrevoteWait(ctx, stateData, EnterPrevoteWaitEvent{Height: height, Round: vote.Round})
			}

		case stateData.Proposal != nil && 0 <= stateData.Proposal.POLRound && stateData.Proposal.POLRound == vote.Round:
			// If the proposal is now complete, enter prevote of cs.Round.
			if stateData.isProposalComplete() {
				_ = behavior.EnterPrevote(ctx, stateData, EnterPrevoteEvent{Height: height, Round: stateData.Round})
			}
		}

	case tmproto.PrecommitType:
		precommits := stateData.Votes.Precommits(vote.Round)
		cs.logger.Debug("added vote to precommit",
			"height", vote.Height,
			"round", vote.Round,
			"validator", vote.ValidatorProTxHash.String(),
			"val_index", vote.ValidatorIndex,
			"data", precommits.LogString())

		blockID, ok := precommits.TwoThirdsMajority()
		if ok {
			// Executed as TwoThirdsMajority could be from a higher round
			_ = behavior.EnterNewRound(ctx, stateData, EnterNewRoundEvent{Height: height, Round: vote.Round})
			_ = behavior.EnterPrecommit(ctx, stateData, EnterPrecommitEvent{Height: height, Round: vote.Round})

			if !blockID.IsNil() {
				_ = behavior.EnterCommit(ctx, stateData, EnterCommitEvent{Height: height, CommitRound: vote.Round})
				if stateData.bypassCommitTimeout() && precommits.HasAll() {
					_ = behavior.EnterNewRound(ctx, stateData, EnterNewRoundEvent{Height: stateData.Height})
				}
			} else {
				behavior.EnterPrecommitWait(ctx, stateData, EnterPrecommitWaitEvent{Height: height, Round: vote.Round})
			}
		} else if stateData.Round <= vote.Round && precommits.HasTwoThirdsAny() {
			_ = behavior.EnterNewRound(ctx, stateData, EnterNewRoundEvent{Height: height, Round: vote.Round})
			behavior.EnterPrecommitWait(ctx, stateData, EnterPrecommitWaitEvent{Height: height, Round: vote.Round})
		}

	default:
		panic(fmt.Sprintf("unexpected vote type %v", vote.Type))
	}

	return added, err
}

func (cs *TryAddVoteCommand) Subscribe(observer *Observer) {
	observer.Subscribe(SetMetrics, func(a any) error {
		cs.metrics = a.(*Metrics)
		return nil
	})
}
