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
func (cs *TryAddVoteCommand) Execute(ctx context.Context, behaviour *Behaviour, stateEvent StateEvent) (any, error) {
	appState := stateEvent.AppState
	event := stateEvent.Data.(TryAddVoteEvent)
	vote, peerID := event.Vote, event.PeerID
	added, err := cs.addVote(ctx, behaviour, appState, vote, peerID)
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
			cs.logger.Info("failed attempting to add vote", "quorum_hash", appState.Validators.QuorumHash, "err", err)
			return added, ErrAddingVote
		}
	}

	return added, nil
}

func (cs *TryAddVoteCommand) addVote(
	ctx context.Context,
	behaviour *Behaviour,
	appState *AppState,
	vote *types.Vote,
	peerID types.NodeID,
) (added bool, err error) {
	cs.logger.Debug(
		"adding vote",
		"vote", vote,
		"height", appState.Height,
		"round", appState.Round,
	)

	// A precommit for the previous height?
	// These come in while we wait timeoutCommit
	if vote.Height+1 == appState.Height && vote.Type == tmproto.PrecommitType {
		if appState.Step != cstypes.RoundStepNewHeight {
			// Late precommit at prior height is ignored
			cs.logger.Debug("precommit vote came in after commit timeout and has been ignored", "vote", vote)
			return
		}
		if appState.LastPrecommits == nil {
			cs.logger.Debug("no last round precommits on node", "vote", vote)
			return
		}

		added, err = appState.LastPrecommits.AddVote(vote)
		if !added {
			cs.logger.Debug(
				"vote not added",
				"height", vote.Height,
				"vote_type", vote.Type,
				"val_index", vote.ValidatorIndex,
				"cs_height", appState.Height,
				"error", err,
			)
			return
		}

		cs.logger.Debug("added vote to last precommits", "last_precommits", appState.LastPrecommits.StringShort())

		err = cs.eventPublisher.PublishVoteEvent(vote)
		if err != nil {
			return added, err
		}

		// if we can skip timeoutCommit and have all the votes now,
		if appState.bypassCommitTimeout() && appState.LastPrecommits.HasAll() {
			// go straight to new round (skip timeout commit)
			// cs.scheduleTimeout(time.Duration(0), cs.Height, 0, cstypes.RoundStepNewHeight)
			_ = behaviour.EnterNewRound(ctx, appState, EnterNewRoundEvent{Height: appState.Height})
		}

		return
	}

	// Height mismatch is ignored.
	// Not necessarily a bad peer, but not favorable behavior.
	if vote.Height != appState.Height {
		added = false
		cs.logger.Debug("vote ignored and not added", "vote_height", vote.Height, "cs_height", appState.Height, "peer", peerID)
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
		val := appState.state.Validators.GetByIndex(vote.ValidatorIndex)
		qt, qh := appState.state.Validators.QuorumType, appState.state.Validators.QuorumHash
		if err := vote.VerifyExtensionSign(appState.state.ChainID, val.PubKey, qt, qh); err != nil {
			return false, err
		}

		err := cs.blockExec.VerifyVoteExtension(ctx, vote)
		cs.metrics.MarkVoteExtensionReceived(err == nil)
		if err != nil {
			return false, err
		}
	}

	// Ignore vote if we do not have public keys to verify votes
	if !appState.Validators.HasPublicKeys {
		added = false
		cs.logger.Debug("vote received on non-validator, ignoring it",
			"vote", vote,
			"cs_height", appState.Height,
			"peer", peerID,
		)
		return
	}

	cs.logger.Debug(
		"adding vote to vote set",
		"height", appState.Height,
		"round", appState.Round,
		"vote", vote,
	)

	height := appState.Height
	added, err = appState.Votes.AddVote(vote, peerID)
	if !added {
		if err != nil {
			cs.logger.Error(
				"error adding vote",
				"vote", vote,
				"cs_height", appState.Height,
				"error", err,
			)
		}
		// Either duplicate, or error upon cs.Votes.AddByIndex()
		return
	}
	if vote.Round == appState.Round {
		vals := appState.state.Validators
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
		prevotes := appState.Votes.Prevotes(vote.Round)
		cs.logger.Debug("added vote to prevote", "vote", vote, "prevotes", prevotes.StringShort())

		// Check to see if >2/3 of the voting power on the network voted for any non-nil block.
		if blockID, ok := prevotes.TwoThirdsMajority(); ok && !blockID.IsNil() {
			// Greater than 2/3 of the voting power on the network voted for some
			// non-nil block

			// Update Valid* if we can.
			if appState.ValidRound < vote.Round && vote.Round == appState.Round {
				if appState.ProposalBlock.HashesTo(blockID.Hash) {
					cs.logger.Debug("updating valid block because of POL", "valid_round", appState.ValidRound, "pol_round", vote.Round)
					appState.ValidRound = vote.Round
					appState.ValidBlock = appState.ProposalBlock
					appState.ValidBlockParts = appState.ProposalBlockParts
				} else {
					cs.logger.Debug("valid block we do not know about; set ProposalBlock=nil",
						"proposal", tmstrings.LazyBlockHash(appState.ProposalBlock),
						"block_id", blockID.Hash)

					// we're getting the wrong block
					appState.ProposalBlock = nil
				}

				if !appState.ProposalBlockParts.HasHeader(blockID.PartSetHeader) {
					cs.metrics.MarkBlockGossipStarted()
					appState.ProposalBlockParts = types.NewPartSetFromHeader(blockID.PartSetHeader)
				}
				err = appState.Save()
				if err != nil {
					return false, err
				}
				cs.eventPublisher.PublishValidBlockEvent(appState.RoundState)
			}
		}

		// If +2/3 prevotes for *anything* for future round:
		switch {
		case appState.Round < vote.Round && prevotes.HasTwoThirdsAny():
			// Round-skip if there is any 2/3+ of votes ahead of us
			_ = behaviour.EnterNewRound(ctx, appState, EnterNewRoundEvent{Height: height, Round: vote.Round})

		case appState.Round == vote.Round && cstypes.RoundStepPrevote <= appState.Step: // current round
			blockID, ok := prevotes.TwoThirdsMajority()
			if ok && (appState.isProposalComplete() || blockID.IsNil()) {
				_ = behaviour.EnterPrecommit(ctx, appState, EnterPrecommitEvent{Height: height, Round: vote.Round})
			} else if prevotes.HasTwoThirdsAny() {
				behaviour.EnterPrevoteWait(ctx, appState, EnterPrevoteWaitEvent{Height: height, Round: vote.Round})
			}

		case appState.Proposal != nil && 0 <= appState.Proposal.POLRound && appState.Proposal.POLRound == vote.Round:
			// If the proposal is now complete, enter prevote of cs.Round.
			if appState.isProposalComplete() {
				_ = behaviour.EnterPrevote(ctx, appState, EnterPrevoteEvent{Height: height, Round: appState.Round})
			}
		}

	case tmproto.PrecommitType:
		precommits := appState.Votes.Precommits(vote.Round)
		cs.logger.Debug("added vote to precommit",
			"height", vote.Height,
			"round", vote.Round,
			"validator", vote.ValidatorProTxHash.String(),
			"val_index", vote.ValidatorIndex,
			"data", precommits.LogString())

		blockID, ok := precommits.TwoThirdsMajority()
		if ok {
			// Executed as TwoThirdsMajority could be from a higher round
			_ = behaviour.EnterNewRound(ctx, appState, EnterNewRoundEvent{Height: height, Round: vote.Round})
			_ = behaviour.EnterPrecommit(ctx, appState, EnterPrecommitEvent{Height: height, Round: vote.Round})

			if !blockID.IsNil() {
				_ = behaviour.EnterCommit(ctx, appState, EnterCommitEvent{Height: height, CommitRound: vote.Round})
				if appState.bypassCommitTimeout() && precommits.HasAll() {
					_ = behaviour.EnterNewRound(ctx, appState, EnterNewRoundEvent{Height: appState.Height})
				}
			} else {
				behaviour.EnterPrecommitWait(ctx, appState, EnterPrecommitWaitEvent{Height: height, Round: vote.Round})
			}
		} else if appState.Round <= vote.Round && precommits.HasTwoThirdsAny() {
			_ = behaviour.EnterNewRound(ctx, appState, EnterNewRoundEvent{Height: height, Round: vote.Round})
			behaviour.EnterPrecommitWait(ctx, appState, EnterPrecommitWaitEvent{Height: height, Round: vote.Round})
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
