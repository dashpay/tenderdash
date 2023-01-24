package consensus

import (
	"context"
	"errors"
	"github.com/tendermint/tendermint/libs/events"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/libs/log"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
)

type (
	AddVoteFunc           func(ctx context.Context, stateData *StateData, vote *types.Vote) (bool, error)
	AddVoteMiddlewareFunc func(next AddVoteFunc) AddVoteFunc
)

type AddVoteEvent struct {
	Vote   *types.Vote
	PeerID types.NodeID
}

// GetType returns AddVoteType event-type
func (e *AddVoteEvent) GetType() EventType {
	return AddVoteType
}

// AddVoteCommand is the command to add a vote to the vote-set
// Attempt to add the vote. if its a duplicate signature, dupeout the validator
type AddVoteCommand struct {
	prevote   AddVoteFunc
	precommit AddVoteFunc
}

// Execute adds received vote to prevote or precommit set
func (c *AddVoteCommand) Execute(ctx context.Context, stateEvent StateEvent) error {
	stateData := stateEvent.StateData
	event := stateEvent.Data.(*AddVoteEvent)
	vote := event.Vote
	var err error
	switch vote.Type {
	case tmproto.PrevoteType:
		_, err = c.prevote(ctx, stateData, event.Vote)
	case tmproto.PrecommitType:
		_, err = c.precommit(ctx, stateData, event.Vote)
	}
	return err
}

// addVoteToVoteSet adds a vote to the vote-set
func addVoteToVoteSet(metrics *Metrics, ep *EventPublisher) AddVoteFunc {
	return func(ctx context.Context, stateData *StateData, vote *types.Vote) (bool, error) {
		added, err := stateData.Votes.AddVote(vote)
		if !added || err != nil {
			return added, err
		}
		if vote.Round == stateData.Round {
			vals := stateData.state.Validators
			val := vals.GetByIndex(vote.ValidatorIndex)
			metrics.MarkVoteReceived(vote.Type, val.VotingPower, vals.TotalVotingPower())
		}
		err = ep.PublishVoteEvent(vote)
		return true, err
	}
}

func addVoteToLastPrecommitMw(logger log.Logger, ep *EventPublisher, fsm *FSM) AddVoteMiddlewareFunc {
	return func(next AddVoteFunc) AddVoteFunc {
		return func(ctx context.Context, stateData *StateData, vote *types.Vote) (bool, error) {
			if vote.Height+1 != stateData.Height || vote.Type != tmproto.PrecommitType {
				return next(ctx, stateData, vote)
			}
			logKeyVals := logKeyValsFromCtx(ctx)
			if stateData.Step != cstypes.RoundStepNewHeight {
				// Late precommit at prior height is ignored
				logger.Debug("precommit vote came in after commit timeout and has been ignored", logKeyVals...)
				return false, nil
			}
			if stateData.LastPrecommits == nil {
				logger.Debug("no last round precommits on node", "vote", vote)
				return false, nil
			}
			added, err := stateData.LastPrecommits.AddVote(vote)
			if !added {
				if err != nil {
					logKeyVals = append(logKeyVals, "error", err)
				}
				logger.Debug("vote not added to last precommits", logKeyVals...)
				return false, nil
			}
			logger.Debug("added vote to last precommits", append(logKeyVals, "last_precommits", stateData.LastPrecommits.StringShort())...)

			err = ep.PublishVoteEvent(vote)
			if err != nil {
				return added, err
			}

			// if we can skip timeoutCommit and have all the votes now,
			if stateData.bypassCommitTimeout() && stateData.LastPrecommits.HasAll() {
				// go straight to new round (skip timeout commit)
				// c.scheduleTimeout(time.Duration(0), c.Height, 0, cstypes.RoundStepNewHeight)
				_ = fsm.Dispatch(ctx, &EnterNewRoundEvent{Height: stateData.Height}, stateData)
			}
			return added, err
		}
	}
}

func addVoteUpdateValidBlockMw(ep *EventPublisher) AddVoteMiddlewareFunc {
	return func(next AddVoteFunc) AddVoteFunc {
		return func(ctx context.Context, stateData *StateData, vote *types.Vote) (bool, error) {
			added, err := next(ctx, stateData, vote)
			if !added || err != nil {
				return added, err
			}
			prevotes := stateData.Votes.Prevotes(vote.Round)
			// Check to see if >2/3 of the voting power on the network voted for any non-nil block.
			blockID, ok := prevotes.TwoThirdsMajority()
			if ok && !blockID.IsNil() {
				// Greater than 2/3 of the voting power on the network voted for some
				// non-nil block

				// Update Valid* if we can.
				stateData.updateValidBlockIfBlockIDMatches(blockID, vote.Round)
				err = stateData.Save()
				if err != nil {
					return added, err
				}
				ep.PublishValidBlockEvent(stateData.RoundState)
			}
			return added, err
		}
	}
}

func addVoteDispatchPrevoteMw(fsm *FSM) AddVoteMiddlewareFunc {
	return func(next AddVoteFunc) AddVoteFunc {
		return func(ctx context.Context, stateData *StateData, vote *types.Vote) (bool, error) {
			added, err := next(ctx, stateData, vote)
			if !added || err != nil || vote.Type != tmproto.PrevoteType {
				return added, err
			}
			prevotes := stateData.Votes.Prevotes(vote.Round)
			proposal := stateData.Proposal
			height := stateData.Height
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

			case proposal != nil && 0 <= proposal.POLRound && proposal.POLRound == vote.Round && stateData.isProposalComplete():
				// If the proposal is now complete, enter prevote of c.Round.
				_ = fsm.Dispatch(ctx, &EnterPrevoteEvent{Height: height, Round: stateData.Round}, stateData)
			}
			return added, err
		}
	}
}

func addVoteDispatchPrecommitMw(FSM *FSM) AddVoteMiddlewareFunc {
	return func(next AddVoteFunc) AddVoteFunc {
		return func(ctx context.Context, stateData *StateData, vote *types.Vote) (bool, error) {
			added, err := next(ctx, stateData, vote)
			if !added || err != nil || vote.Type != tmproto.PrecommitType {
				return added, err
			}

			precommits := stateData.Votes.Precommits(vote.Round)
			height := stateData.Height

			blockID, ok := precommits.TwoThirdsMajority()
			if !ok && stateData.Round <= vote.Round && precommits.HasTwoThirdsAny() {
				_ = FSM.Dispatch(ctx, &EnterNewRoundEvent{Height: height, Round: vote.Round}, stateData)
				_ = FSM.Dispatch(ctx, &EnterPrecommitWaitEvent{Height: height, Round: vote.Round}, stateData)
				return added, err
			}
			if !ok {
				return added, err
			}
			// Executed as TwoThirdsMajority could be from a higher round
			_ = FSM.Dispatch(ctx, &EnterNewRoundEvent{Height: height, Round: vote.Round}, stateData)
			_ = FSM.Dispatch(ctx, &EnterPrecommitEvent{Height: height, Round: vote.Round}, stateData)

			if blockID.IsNil() {
				_ = FSM.Dispatch(ctx, &EnterPrecommitWaitEvent{Height: height, Round: vote.Round}, stateData)
				return added, err
			}
			_ = FSM.Dispatch(ctx, &EnterCommitEvent{Height: height, CommitRound: vote.Round}, stateData)
			if stateData.bypassCommitTimeout() && precommits.HasAll() {
				_ = FSM.Dispatch(ctx, &EnterNewRoundEvent{Height: stateData.Height}, stateData)
			}
			return added, err
		}
	}
}

func addVoteVerifyVoteExtensionMw(
	privVal privValidator,
	blockExec *sm.BlockExecutor,
	metrics *Metrics,
	evsw events.EventSwitch,
) AddVoteMiddlewareFunc {
	_ = evsw.AddListenerForEvent("addVoteVerifyVoteExtensionMw", setPrivValidator, func(data events.EventData) error {
		privVal = data.(privValidator)
		return nil
	})
	return func(next AddVoteFunc) AddVoteFunc {
		return func(ctx context.Context, stateData *StateData, vote *types.Vote) (bool, error) {
			// Verify VoteExtension if precommit and not nil
			// https://github.com/tendermint/tendermint/issues/8487
			if vote.Type != tmproto.PrecommitType ||
				vote.BlockID.IsNil() ||
				privVal.IsProTxHashEqual(vote.ValidatorProTxHash) {
				// Skip the VerifyVoteExtension call if the vote was issued by this validator.
				return next(ctx, stateData, vote)
			}

			// The core fields of the vote message were already validated in the
			// consensus reactor when the vote was received.
			// Here, we verify the signature of the vote extension included in the vote
			// message.
			val := stateData.state.Validators.GetByIndex(vote.ValidatorIndex)
			qt, qh := stateData.state.Validators.QuorumType, stateData.state.Validators.QuorumHash
			if err := vote.VerifyExtensionSign(stateData.state.ChainID, val.PubKey, qt, qh); err != nil {
				return false, err
			}
			err := blockExec.VerifyVoteExtension(ctx, vote)
			metrics.MarkVoteExtensionReceived(err == nil)
			if err != nil {
				return false, err
			}
			return next(ctx, stateData, vote)
		}
	}
}

func addVoteValidateVoteMw() AddVoteMiddlewareFunc {
	return func(next AddVoteFunc) AddVoteFunc {
		return func(ctx context.Context, stateData *StateData, vote *types.Vote) (bool, error) {
			// Height mismatch is ignored.
			// Not necessarily a bad peer, but not favorable behavior.
			if vote.Height != stateData.Height {
				return false, nil
			}
			// Ignore vote if we do not have public keys to verify votes
			if !stateData.Validators.HasPublicKeys {
				return false, nil
			}
			return next(ctx, stateData, vote)
		}
	}
}

func addVoteStatsMw(statsQueue *chanQueue[msgInfo]) AddVoteMiddlewareFunc {
	return func(next AddVoteFunc) AddVoteFunc {
		return func(ctx context.Context, stateData *StateData, vote *types.Vote) (bool, error) {
			added, err := next(ctx, stateData, vote)
			if added {
				_ = statsQueue.send(ctx, msgInfoFromCtx(ctx))
			}
			return added, err
		}
	}
}

// add evidence to the pool
// when it's detected
func addVoteErrorMw(evpool evidencePool, logger log.Logger, privVal privValidator, evsw events.EventSwitch) AddVoteMiddlewareFunc {
	_ = evsw.AddListenerForEvent("addVoteErrorMw", setPrivValidator, func(data events.EventData) error {
		privVal = data.(privValidator)
		return nil
	})
	return func(next AddVoteFunc) AddVoteFunc {
		return func(ctx context.Context, stateData *StateData, vote *types.Vote) (bool, error) {
			added, err := next(ctx, stateData, vote)
			if err == nil {
				return added, err
			}
			// If the vote height is off, we'll just ignore it,
			// But if it's a conflicting sig, add it to the c.evpool.
			// If it's otherwise invalid, punish peer.
			voteErr, ok := err.(*types.ErrVoteConflictingVotes)
			if !ok {
				return added, err
			}
			if privVal.IsZero() {
				return added, ErrPrivValidatorNotSet
			}
			if privVal.IsProTxHashEqual(vote.ValidatorProTxHash) {
				logger.Error("found conflicting vote from ourselves; did you unsafe_reset a validator?",
					"height", vote.Height,
					"round", vote.Round,
					"type", vote.Type)

				return added, err
			}

			// report conflicting votes to the evidence pool
			evpool.ReportConflictingVotes(voteErr.VoteA, voteErr.VoteB)
			logger.Debug("found and sent conflicting votes to the evidence pool",
				"vote_a", voteErr.VoteA,
				"vote_b", voteErr.VoteB)
			return added, err
		}
	}
}

func addVoteLoggingMw(logger log.Logger) AddVoteMiddlewareFunc {
	return func(next AddVoteFunc) AddVoteFunc {
		return func(ctx context.Context, stateData *StateData, vote *types.Vote) (bool, error) {
			logKeyVals := logKeyValsFromCtx(ctx)
			logger.Debug("adding vote to vote set", logKeyVals...)
			added, err := next(ctx, stateData, vote)
			if !added {
				if err != nil {
					logger.Error("vote not added", append(logKeyVals, "error", err))
					if errors.Is(err, types.ErrVoteNonDeterministicSignature) {
						logger.Debug("vote has non-deterministic signature", "error", err)
						return added, err
					}
					// Either
					// 1) bad peer OR
					// 2) not a bad peer? this can also err sometimes with "Unexpected step" OR
					// 3) tmkms use with multiple validators connecting to a single tmkms instance
					//		(https://github.com/tendermint/tendermint/issues/3839).
					logger.Info("failed attempting to add vote", "quorum_hash", stateData.Validators.QuorumHash, "err", err)
					// return added, ErrAddingVote
				}
				return added, err
			}
			votes := stateData.Votes.GetVoteSet(vote.Round, vote.Type)
			logger.Debug("vote added", append(logKeyVals, []any{"data", votes.LogString()})...)
			return added, err
		}
	}
}

func withVoterMws(fn AddVoteFunc, mws ...AddVoteMiddlewareFunc) AddVoteFunc {
	for _, mw := range mws {
		fn = mw(fn)
	}
	return fn
}
