package consensus

import (
	"context"
	"errors"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	tmstrings "github.com/dashpay/tenderdash/internal/libs/strings"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
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

// AddVoteAction is the command to add a vote to the vote-set
// Attempt to add the vote. if its a duplicate signature, dupeout the validator
type AddVoteAction struct {
	prevote   AddVoteFunc
	precommit AddVoteFunc
}

func newAddVoteAction(cs *State, ctrl *Controller, statsQueue *chanQueue[msgInfo]) *AddVoteAction {
	addToVoteSet := addVoteToVoteSetFunc(cs.metrics, cs.eventPublisher)
	loggingMw := addVoteLoggingMw()
	updateValidBlockMw := addVoteUpdateValidBlockMw(cs.eventPublisher)
	dispatchPrevoteMw := addVoteDispatchPrevoteMw(ctrl)
	validateVoteMw := addVoteValidateVoteMw()
	errorMw := addVoteErrorMw(cs.evpool, cs.logger, cs.privValidator, cs.emitter)
	statsMw := addVoteStatsMw(statsQueue)
	dispatchPrecommitMw := addVoteDispatchPrecommitMw(ctrl)
	verifyVoteExtensionMw := addVoteVerifyVoteExtensionMw(cs.privValidator, cs.blockExec, cs.metrics, cs.emitter)
	return &AddVoteAction{
		prevote: withVoterMws(
			addToVoteSet,
			loggingMw,
			updateValidBlockMw,
			dispatchPrevoteMw,
			validateVoteMw,
			errorMw,
			statsMw,
		),
		precommit: withVoterMws(
			addToVoteSet,
			loggingMw,
			dispatchPrecommitMw,
			verifyVoteExtensionMw,
			validateVoteMw,
			errorMw,
			statsMw,
		),
	}
}

// Execute adds received vote to prevote or precommit set
func (c *AddVoteAction) Execute(ctx context.Context, stateEvent StateEvent) error {
	stateData := stateEvent.StateData
	event := stateEvent.Data.(*AddVoteEvent)
	vote := event.Vote
	var err error
	switch vote.Type {
	case tmproto.PrevoteType:
		_, err = c.prevote(ctx, stateData, vote)
	case tmproto.PrecommitType:
		_, err = c.precommit(ctx, stateData, vote)
	}
	return err
}

// addVoteToVoteSetFunc adds a vote to the vote-set
func addVoteToVoteSetFunc(metrics *Metrics, ep *EventPublisher) AddVoteFunc {
	return func(_ctx context.Context, stateData *StateData, vote *types.Vote) (bool, error) {
		added, err := stateData.Votes.AddVote(vote)
		if !added || err != nil {
			return added, err
		}
		if vote.Round == stateData.Round {
			vals := stateData.state.Validators
			val := vals.GetByIndex(vote.ValidatorIndex)
			metrics.MarkVoteReceived(vote.Type, val.VotingPower, vals.TotalVotingPower())
		}
		_ = ep.PublishVoteEvent(vote)
		return true, nil
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
			if !ok || blockID.IsNil() {
				return added, err
			}
			// Greater than 2/3 of the voting power on the network voted for some
			// non-nil block
			// Update Valid* if we can.
			if stateData.ValidRound >= vote.Round || vote.Round != stateData.Round {
				return added, err
			}
			logger := log.FromCtxOrNop(ctx)
			if stateData.ProposalBlock.HashesTo(blockID.Hash) {
				logger.Debug("updating valid block because of POL",
					"valid_round", stateData.ValidRound,
					"pol_round", vote.Round)
				stateData.updateValidBlock()
			} else {
				logger.Debug("valid block we do not know about; set ProposalBlock=nil",
					"proposal", tmstrings.LazyBlockHash(stateData.ProposalBlock),
					"block_id", blockID.Hash)
				// we're getting the wrong block
				stateData.ProposalBlock = nil
			}
			if !stateData.ProposalBlockParts.HasHeader(blockID.PartSetHeader) {
				//c.metrics.MarkBlockGossipStarted()
				stateData.ProposalBlockParts = types.NewPartSetFromHeader(blockID.PartSetHeader)
			}
			err = stateData.Save()
			if err != nil {
				return added, err
			}
			ep.PublishValidBlockEvent(stateData.RoundState)
			return added, err
		}
	}
}

// addVoteDispatchPrevoteMw executes one of these transitions "enter new round" OR "enter precommit" OR "enter prevote"
// based on the current state
func addVoteDispatchPrevoteMw(ctrl *Controller) AddVoteMiddlewareFunc {
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
				_ = ctrl.Dispatch(ctx, &EnterNewRoundEvent{Height: height, Round: vote.Round}, stateData)

			case stateData.Round == vote.Round && cstypes.RoundStepPrevote <= stateData.Step: // current round
				blockID, ok := prevotes.TwoThirdsMajority()
				if ok && (stateData.isProposalComplete() || blockID.IsNil()) {
					_ = ctrl.Dispatch(ctx, &EnterPrecommitEvent{Height: height, Round: vote.Round}, stateData)
				} else if prevotes.HasTwoThirdsAny() {
					_ = ctrl.Dispatch(ctx, &EnterPrevoteWaitEvent{Height: height, Round: vote.Round}, stateData)
				}

			case proposal != nil && 0 <= proposal.POLRound && proposal.POLRound == vote.Round && stateData.isProposalComplete():
				// If the proposal is now complete, enter prevote of c.Round.
				_ = ctrl.Dispatch(ctx, &EnterPrevoteEvent{Height: height, Round: stateData.Round}, stateData)
			}
			return added, err
		}
	}
}

func addVoteDispatchPrecommitMw(ctrl *Controller) AddVoteMiddlewareFunc {
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
				_ = ctrl.Dispatch(ctx, &EnterNewRoundEvent{Height: height, Round: vote.Round}, stateData)
				_ = ctrl.Dispatch(ctx, &EnterPrecommitWaitEvent{Height: height, Round: vote.Round}, stateData)
				return added, err
			}
			if !ok {
				return added, err
			}
			// Executed as TwoThirdsMajority could be from a higher round
			_ = ctrl.Dispatch(ctx, &EnterNewRoundEvent{Height: height, Round: vote.Round}, stateData)
			_ = ctrl.Dispatch(ctx, &EnterPrecommitEvent{Height: height, Round: vote.Round}, stateData)

			if blockID.IsNil() {
				_ = ctrl.Dispatch(ctx, &EnterPrecommitWaitEvent{Height: height, Round: vote.Round}, stateData)
				return added, err
			}
			_ = ctrl.Dispatch(ctx, &EnterCommitEvent{Height: height, CommitRound: vote.Round}, stateData)
			if precommits.HasTwoThirdsMajority() {
				_ = ctrl.Dispatch(ctx, &EnterNewRoundEvent{Height: stateData.Height}, stateData)
			}
			return added, err
		}
	}
}

func addVoteVerifyVoteExtensionMw(
	privVal privValidator,
	blockExec *sm.BlockExecutor,
	metrics *Metrics,
	evsw *eventemitter.EventEmitter,
) AddVoteMiddlewareFunc {
	evsw.AddListener(setPrivValidatorEventName, func(data eventemitter.EventData) error {
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
func addVoteErrorMw(evpool evidencePool, logger log.Logger, privVal privValidator, emitter *eventemitter.EventEmitter) AddVoteMiddlewareFunc {
	emitter.AddListener(setPrivValidatorEventName, func(data eventemitter.EventData) error {
		privVal = data.(privValidator)
		return nil
	})
	return func(next AddVoteFunc) AddVoteFunc {
		return func(ctx context.Context, stateData *StateData, vote *types.Vote) (bool, error) {
			added, err := next(ctx, stateData, vote)
			if err == nil {
				return added, err
			}
			if errors.Is(err, types.ErrVoteNonDeterministicSignature) {
				logger.Error("vote has non-deterministic signature", "err", err)
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
			logger.Error("found and sent conflicting votes to the evidence pool",
				"vote_a", voteErr.VoteA,
				"vote_b", voteErr.VoteB)
			return added, err
		}
	}
}

func addVoteLoggingMw() AddVoteMiddlewareFunc {
	return func(next AddVoteFunc) AddVoteFunc {
		return func(ctx context.Context, stateData *StateData, vote *types.Vote) (bool, error) {
			logger := log.FromCtxOrNop(ctx)
			logger.Trace("adding vote to vote set")
			added, err := next(ctx, stateData, vote)
			if !added {
				if err != nil {
					logger.Error("vote not added", "error", err)
					if errors.Is(err, types.ErrVoteNonDeterministicSignature) {
						return added, err
					}
					// Either
					// 1) bad peer OR
					// 2) not a bad peer? this can also err sometimes with "Unexpected step" OR
					// 3) tmkms use with multiple validators connecting to a single tmkms instance
					//		(https://github.com/tendermint/tendermint/issues/3839).
					// return added, ErrAddingVote
				}
				return added, err
			}
			votes := stateData.Votes.GetVoteSet(vote.Round, vote.Type)
			logger.Trace("vote added", "data", votes)
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
