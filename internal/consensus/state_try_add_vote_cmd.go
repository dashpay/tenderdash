package consensus

import (
	"context"
	"errors"
	"fmt"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	tmstrings "github.com/tendermint/tendermint/internal/libs/strings"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/libs/events"
	"github.com/tendermint/tendermint/libs/log"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
)

type TryAddVoteEvent struct {
	Vote   *types.Vote
	PeerID types.NodeID
}

// GetType returns TryAddVoteType event-type
func (e *TryAddVoteEvent) GetType() EventType {
	return TryAddVoteType
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
	statsQueue     *chanQueue[msgInfo]
}

// Execute ...
func (c *TryAddVoteCommand) Execute(ctx context.Context, stateEvent StateEvent) error {
	stateData := stateEvent.StateData
	event := stateEvent.Data.(*TryAddVoteEvent)
	vote, peerID := event.Vote, event.PeerID
	var (
		added bool
		err   error
	)
	defer func() {
		if added {
			_ = c.statsQueue.send(ctx, msgInfoFromCtx(ctx))
		}
	}()
	added, err = c.addVote(ctx, stateEvent.FSM, stateData, vote, peerID)
	if err != nil {
		// If the vote height is off, we'll just ignore it,
		// But if it's a conflicting sig, add it to the c.evpool.
		// If it's otherwise invalid, punish peer.
		if voteErr, ok := err.(*types.ErrVoteConflictingVotes); ok {
			if c.privValidator.IsZero() {
				return ErrPrivValidatorNotSet
			}

			if c.privValidator.IsProTxHashEqual(vote.ValidatorProTxHash) {
				c.logger.Error(
					"found conflicting vote from ourselves; did you unsafe_reset a validator?",
					"height", vote.Height,
					"round", vote.Round,
					"type", vote.Type)

				return err
			}

			// report conflicting votes to the evidence pool
			c.evpool.ReportConflictingVotes(voteErr.VoteA, voteErr.VoteB)
			c.logger.Debug("found and sent conflicting votes to the evidence pool",
				"vote_a", voteErr.VoteA,
				"vote_b", voteErr.VoteB)

			return err
		} else if errors.Is(err, types.ErrVoteNonDeterministicSignature) {
			c.logger.Debug("vote has non-deterministic signature", "err", err)
		} else {
			// Either
			// 1) bad peer OR
			// 2) not a bad peer? this can also err sometimes with "Unexpected step" OR
			// 3) tmkms use with multiple validators connecting to a single tmkms instance
			//		(https://github.com/tendermint/tendermint/issues/3839).
			c.logger.Info("failed attempting to add vote", "quorum_hash", stateData.Validators.QuorumHash, "err", err)
			return ErrAddingVote
		}
	}

	return nil
}

func (c *TryAddVoteCommand) addVote(
	ctx context.Context,
	fsm *FSM,
	stateData *StateData,
	vote *types.Vote,
	peerID types.NodeID,
) (added bool, err error) {
	c.logger.Debug(
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
			c.logger.Debug("precommit vote came in after commit timeout and has been ignored", "vote", vote)
			return
		}
		if stateData.LastPrecommits == nil {
			c.logger.Debug("no last round precommits on node", "vote", vote)
			return
		}

		added, err = stateData.LastPrecommits.AddVote(vote)
		if !added {
			c.logger.Debug(
				"vote not added",
				"height", vote.Height,
				"vote_type", vote.Type,
				"val_index", vote.ValidatorIndex,
				"cs_height", stateData.Height,
				"error", err,
			)
			return
		}

		c.logger.Debug("added vote to last precommits", "last_precommits", stateData.LastPrecommits.StringShort())

		err = c.eventPublisher.PublishVoteEvent(vote)
		if err != nil {
			return added, err
		}

		// if we can skip timeoutCommit and have all the votes now,
		if stateData.bypassCommitTimeout() && stateData.LastPrecommits.HasAll() {
			// go straight to new round (skip timeout commit)
			// c.scheduleTimeout(time.Duration(0), c.Height, 0, cstypes.RoundStepNewHeight)
			_ = fsm.Dispatch(ctx, &EnterNewRoundEvent{Height: stateData.Height}, stateData)
		}

		return
	}

	// Height mismatch is ignored.
	// Not necessarily a bad peer, but not favorable behavior.
	if vote.Height != stateData.Height {
		added = false
		c.logger.Debug("vote ignored and not added", "vote_height", vote.Height, "cs_height", stateData.Height, "peer", peerID)
		return
	}

	// Verify VoteExtension if precommit and not nil
	// https://github.com/tendermint/tendermint/issues/8487
	if vote.Type == tmproto.PrecommitType && !vote.BlockID.IsNil() &&
		!c.privValidator.IsProTxHashEqual(vote.ValidatorProTxHash) { // Skip the VerifyVoteExtension call if the vote was issued by this validator.

		// The core fields of the vote message were already validated in the
		// consensus reactor when the vote was received.
		// Here, we verify the signature of the vote extension included in the vote
		// message.
		val := stateData.state.Validators.GetByIndex(vote.ValidatorIndex)
		qt, qh := stateData.state.Validators.QuorumType, stateData.state.Validators.QuorumHash
		if err := vote.VerifyExtensionSign(stateData.state.ChainID, val.PubKey, qt, qh); err != nil {
			return false, err
		}

		err := c.blockExec.VerifyVoteExtension(ctx, vote)
		c.metrics.MarkVoteExtensionReceived(err == nil)
		if err != nil {
			return false, err
		}
	}

	// Ignore vote if we do not have public keys to verify votes
	if !stateData.Validators.HasPublicKeys {
		added = false
		c.logger.Debug("vote received on non-validator, ignoring it",
			"vote", vote,
			"cs_height", stateData.Height,
			"peer", peerID,
		)
		return
	}

	c.logger.Debug(
		"adding vote to vote set",
		"height", stateData.Height,
		"round", stateData.Round,
		"vote", vote,
	)

	height := stateData.Height
	added, err = stateData.Votes.AddVote(vote)
	if !added {
		if err != nil {
			c.logger.Error(
				"error adding vote",
				"vote", vote,
				"cs_height", stateData.Height,
				"error", err,
			)
		}
		// Either duplicate, or error upon c.Votes.AddByIndex()
		return
	}
	if vote.Round == stateData.Round {
		vals := stateData.state.Validators
		val := vals.GetByIndex(vote.ValidatorIndex)
		c.metrics.MarkVoteReceived(vote.Type, val.VotingPower, vals.TotalVotingPower())
	}

	// TODO discuss about checking error result
	err = c.eventPublisher.PublishVoteEvent(vote)
	if err != nil {
		return added, err
	}

	switch vote.Type {
	case tmproto.PrevoteType:
		prevotes := stateData.Votes.Prevotes(vote.Round)
		c.logger.Debug("added vote to prevote", "vote", vote, "prevotes", prevotes.StringShort())

		// Check to see if >2/3 of the voting power on the network voted for any non-nil block.
		if blockID, ok := prevotes.TwoThirdsMajority(); ok && !blockID.IsNil() {
			// Greater than 2/3 of the voting power on the network voted for some
			// non-nil block

			// Update Valid* if we can.
			if stateData.ValidRound < vote.Round && vote.Round == stateData.Round {
				if stateData.ProposalBlock.HashesTo(blockID.Hash) {
					c.logger.Debug("updating valid block because of POL", "valid_round", stateData.ValidRound, "pol_round", vote.Round)
					stateData.updateValidBlock()
				} else {
					c.logger.Debug("valid block we do not know about; set ProposalBlock=nil",
						"proposal", tmstrings.LazyBlockHash(stateData.ProposalBlock),
						"block_id", blockID.Hash)

					// we're getting the wrong block
					stateData.ProposalBlock = nil
				}

				if !stateData.ProposalBlockParts.HasHeader(blockID.PartSetHeader) {
					c.metrics.MarkBlockGossipStarted()
					stateData.ProposalBlockParts = types.NewPartSetFromHeader(blockID.PartSetHeader)
				}
				err = stateData.Save()
				if err != nil {
					return false, err
				}
				c.eventPublisher.PublishValidBlockEvent(stateData.RoundState)
			}
		}

		// If +2/3 prevotes for *anything* for future round:
		switch {
		case stateData.Round < vote.Round && prevotes.HasTwoThirdsAny():
			// Round-skip if there is any 2/3+ of votes ahead of us
			_ = fsm.Dispatch(ctx, &EnterNewRoundEvent{Height: height, Round: vote.Round}, stateData)

		case stateData.Round == vote.Round && cstypes.RoundStepPrevote <= stateData.Step: // current round
			blockID, ok := prevotes.TwoThirdsMajority()
			if ok && (stateData.isProposalComplete() || blockID.IsNil()) {
				_ = fsm.Dispatch(ctx, &EnterPrecommitEvent{Height: height, Round: vote.Round}, stateData)
			} else if prevotes.HasTwoThirdsAny() {
				_ = fsm.Dispatch(ctx, &EnterPrevoteWaitEvent{Height: height, Round: vote.Round}, stateData)
			}

		case stateData.Proposal != nil && 0 <= stateData.Proposal.POLRound && stateData.Proposal.POLRound == vote.Round:
			// If the proposal is now complete, enter prevote of c.Round.
			if stateData.isProposalComplete() {
				_ = fsm.Dispatch(ctx, &EnterPrevoteEvent{Height: height, Round: stateData.Round}, stateData)
			}
		}

	case tmproto.PrecommitType:
		precommits := stateData.Votes.Precommits(vote.Round)
		c.logger.Debug("added vote to precommit",
			"height", vote.Height,
			"round", vote.Round,
			"validator", vote.ValidatorProTxHash.String(),
			"val_index", vote.ValidatorIndex,
			"data", precommits.LogString())

		blockID, ok := precommits.TwoThirdsMajority()
		if ok {
			// Executed as TwoThirdsMajority could be from a higher round
			_ = fsm.Dispatch(ctx, &EnterNewRoundEvent{Height: height, Round: vote.Round}, stateData)
			_ = fsm.Dispatch(ctx, &EnterPrecommitEvent{Height: height, Round: vote.Round}, stateData)

			if !blockID.IsNil() {
				_ = fsm.Dispatch(ctx, &EnterCommitEvent{Height: height, CommitRound: vote.Round}, stateData)
				if stateData.bypassCommitTimeout() && precommits.HasAll() {
					_ = fsm.Dispatch(ctx, &EnterNewRoundEvent{Height: stateData.Height}, stateData)
				}
			} else {
				_ = fsm.Dispatch(ctx, &EnterPrecommitWaitEvent{Height: height, Round: vote.Round}, stateData)
			}
		} else if stateData.Round <= vote.Round && precommits.HasTwoThirdsAny() {
			_ = fsm.Dispatch(ctx, &EnterNewRoundEvent{Height: height, Round: vote.Round}, stateData)
			_ = fsm.Dispatch(ctx, &EnterPrecommitWaitEvent{Height: height, Round: vote.Round}, stateData)
		}

	default:
		panic(fmt.Sprintf("unexpected vote type %v", vote.Type))
	}

	return added, err
}

func (c *TryAddVoteCommand) subscribe(evsw events.EventSwitch) {
	_ = evsw.AddListenerForEvent("addVoteCommand", setPrivValidator, func(a events.EventData) error {
		c.privValidator = a.(privValidator)
		return nil
	})
}
