package consensus

import (
	"context"
	"errors"
	"fmt"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

// isPeerFloodableError reports whether err is a message validation failure that
// an unprivileged peer can trigger at will and therefore flood. These are
// logged at Debug so a flood cannot spam Error-level logs. Anything not matched
// here — notably internal faults such as ErrPrivValidatorNotSet — is treated as
// a real problem and stays at Error, so classification errs toward surfacing.
//
// Rejecting a message is not a verdict on its sender: a proposal for a height
// we have moved past, or a block part whose proof does not check out, is what a
// peer produces by being slow or by being hostile, and neither can be told
// apart here. What they have in common is that producing one is free, so a log
// line per rejection is an amplification vector.
func isPeerFloodableError(err error) bool {
	return errors.Is(err, types.ErrVoteInvalidBlockSignature) ||
		errors.Is(err, types.ErrVoteInvalidSignature) ||
		errors.Is(err, types.ErrVoteUnexpectedStep) ||
		// A vote names its validator twice, by index and by pro-tx hash, and a
		// sender is free to make the two disagree. The disagreement is caught
		// before any signature is read, so it is the cheapest rejection a peer
		// can buy.
		errors.Is(err, types.ErrVoteInvalidValidatorProTxHash) ||
		errors.Is(err, types.ErrVerificationBudgetExhausted) ||
		errors.Is(err, types.ErrPartSetInvalidProof) ||
		errors.Is(err, types.ErrPartSetUnexpectedIndex) ||
		errors.Is(err, types.ErrPartSetIndexMismatch) ||
		errors.Is(err, ErrInvalidProposalSignature) ||
		errors.Is(err, ErrInvalidProposalPOLRound) ||
		errors.Is(err, ErrInvalidProposalCoreHeight) ||
		errors.Is(err, ErrInvalidProposalForCommit) ||
		errors.Is(err, ErrUnableToVerifyProposal) ||
		errors.Is(err, ErrPeerStateInvalidVoteIndex) ||
		errors.Is(err, ErrInvalidNewRoundStepHeight)
}

type msgInfoDispatcher struct {
	proposalHandler  msgHandlerFunc
	blockPartHandler msgHandlerFunc
	voteHandler      msgHandlerFunc
	commitHandler    msgHandlerFunc
}

func (c *msgInfoDispatcher) match(m Message) (msgHandlerFunc, error) {
	switch m.(type) {
	case *ProposalMessage:
		return c.proposalHandler, nil
	case *BlockPartMessage:
		return c.blockPartHandler, nil
	case *VoteMessage:
		return c.voteHandler, nil
	case *CommitMessage:
		return c.commitHandler, nil
	}
	return nil, fmt.Errorf("got unknown %T type", m)
}

func (c *msgInfoDispatcher) dispatch(ctx context.Context, stateData *StateData, msg Message, opts ...func(envelope *msgEnvelope)) error {
	var m any = msg
	mi := m.(msgInfo)
	if mi.Msg == nil {
		return nil
	}
	envelope := msgEnvelope{
		msgInfo:    mi,
		fromReplay: false,
	}
	for _, opt := range opts {
		opt(&envelope)
	}
	handler, err := c.match(mi.Msg)
	if err != nil {
		return fmt.Errorf("message handler not found: %w", err)
	}
	return handler(ctx, stateData, envelope)
}

// msgInfoDispatcher creates a new dispatcher for messages that are received from peers.
// It is used to dispatch messages to the appropriate handler.
func newMsgInfoDispatcher(
	ctrl *Controller,
	proposaler cstypes.Proposaler,
	wal WALWriteFlusher,
	logger log.Logger,
	budget types.VerificationBudget,
	middleware ...msgMiddlewareFunc,
) *msgInfoDispatcher {

	mws := []msgMiddlewareFunc{
		msgInfoWithCtxMiddleware(),
		loggingMiddleware(logger),
		walMiddleware(wal, logger),
	}
	mws = append(mws, middleware...)

	proposalHandler := withMiddleware(proposalMessageHandler(proposaler, budget), mws...)
	blockPartHandler := withMiddleware(blockPartMessageHandler(ctrl), mws...)
	voteHandler := withMiddleware(voteMessageHandler(ctrl), mws...)
	commitHandler := withMiddleware(commitMessageHandler(ctrl), mws...)
	return &msgInfoDispatcher{
		proposalHandler:  proposalHandler,
		blockPartHandler: blockPartHandler,
		voteHandler:      voteHandler,
		commitHandler:    commitHandler,
	}
}

// proposalMessageHandler hands a proposal to the setter along with the permit
// its signature check is drawn against. Only a peer's proposal carries one:
// this node's own proposals and those replayed from the write-ahead log are not
// what the budget bounds.
func proposalMessageHandler(propSetter cstypes.ProposalSetter, budget types.VerificationBudget) msgHandlerFunc {
	return func(ctx context.Context, stateData *StateData, envelope msgEnvelope) error {
		msg := envelope.Msg.(*ProposalMessage)
		ctx = ctxWithPeerVerificationBudget(ctx, envelope.PeerID, envelope.fromReplay, budget)
		return propSetter.Set(ctx, msg.Proposal, envelope.ReceiveTime, &stateData.RoundState)
	}
}

func blockPartMessageHandler(ctrl *Controller) msgHandlerFunc {
	return func(ctx context.Context, stateData *StateData, envelope msgEnvelope) error {
		logger := log.FromCtxOrNop(ctx)
		msg := envelope.Msg.(*BlockPartMessage)
		// if the proposal is complete, we'll enterPrevote or tryFinalizeCommit
		err := ctrl.Dispatch(ctx, &AddProposalBlockPartEvent{
			Msg:        msg,
			PeerID:     envelope.PeerID,
			FromReplay: envelope.fromReplay,
		}, stateData)
		if err != nil && msg.Round != stateData.Round {
			logger.Trace("received block part from wrong round")
			return nil
		}
		return err
	}
}

func voteMessageHandler(ctrl *Controller) msgHandlerFunc {
	return func(ctx context.Context, stateData *StateData, envelope msgEnvelope) error {
		msg := envelope.Msg.(*VoteMessage)
		// attempt to add the vote and dupeout the validator if its a duplicate signature
		// if the vote gives us a 2/3-any or 2/3-one, we transition
		err := ctrl.Dispatch(ctx, &AddVoteEvent{
			Vote:       msg.Vote,
			PeerID:     envelope.PeerID,
			FromReplay: envelope.fromReplay,
		}, stateData)

		// TODO: punish peer
		// We probably don't want to stop the peer here. The vote does not
		// necessarily comes from a malicious peer but can be just broadcasted by
		// a typical peer.
		// https://github.com/tendermint/tendermint/issues/1281

		// NOTE: the vote is broadcast to peers by the reactor listening
		// for vote events

		// TODO: If rs.Height == vote.Height && rs.Round < vote.Round,
		// the peer is sending us CatchupCommit precommits.
		// We could make note of this and help filter in broadcastHasVoteMessage().
		return err
	}
}

func commitMessageHandler(ctrl *Controller) msgHandlerFunc {
	return func(ctx context.Context, stateData *StateData, envelope msgEnvelope) error {
		msg := envelope.Msg.(*CommitMessage)
		// attempt to add the commit and dupeout the validator if its a duplicate signature
		// if the vote gives us a 2/3-any or 2/3-one, we transition
		return ctrl.Dispatch(ctx, &TryAddCommitEvent{
			Commit:     msg.Commit,
			PeerID:     envelope.PeerID,
			FromReplay: envelope.fromReplay,
		}, stateData)
	}
}

// verificationBudgetMiddleware covers the window in which the peer scheduler's
// own affordability check cannot have been enough.
//
// The budget is charged in stages as a message is verified, so a later stage can
// be denied after earlier stages already paid for signature checks and an ABCI
// round-trip — throwing away both the work and a valid vote. The scheduler makes
// room for the whole message before handing it over, which closes that window
// for everything it schedules; this check covers what it did not schedule, since
// nothing structurally guarantees a message reached this goroutine through the
// scheduler.
//
// As the queue is wired, every message carrying a peer identity reaches
// this goroutine through the scheduler, and the scheduler hands over one at a
// time, so this normally finds the room already made and returns without
// waiting. It is a backstop, not the gate that does the work: what it still
// catches is a peer message routed past the scheduler, and budget spent between
// the two by the messages this node makes for itself, which take the other queue
// and are never charged.
//
// Local and replayed messages are not charged and therefore never wait, and a
// message that cannot be afforded within the bounded wait is dropped rather than
// reported: local overload says nothing about the sender. Because this is the
// outermost middleware, such a message also costs no write-ahead log record.
func verificationBudgetMiddleware(
	budget types.VerificationBudget,
	metrics *Metrics,
	logger log.Logger,
) msgMiddlewareFunc {
	waiter, _ := budget.(budgetWaiter)
	return func(hd msgHandlerFunc) msgHandlerFunc {
		return func(ctx context.Context, stateData *StateData, envelope msgEnvelope) error {
			if waiter == nil || envelope.PeerID == "" || envelope.fromReplay {
				return hd(ctx, stateData, envelope)
			}
			// Every outcome below returns nil: an error here would stop the
			// consensus goroutine, and none of them is a protocol fault.
			cost, err := budgetedMessageCost(envelope.Msg)
			if err != nil {
				metrics.VerificationBudgetDrops.Add(1)
				logger.Debug("dropping unpriceable peer message", "peer", envelope.PeerID, "error", err)
				return nil
			}
			if !waiter.waitFor(ctx, cost) {
				metrics.VerificationBudgetDrops.Add(1)
				logger.Debug("dropping peer message the verification budget cannot cover",
					"peer", envelope.PeerID, "cost", cost)
				return nil
			}
			return hd(ctx, stateData, envelope)
		}
	}
}

func walMiddleware(wal WALWriteFlusher, logger log.Logger) msgMiddlewareFunc {
	return func(hd msgHandlerFunc) msgHandlerFunc {
		return func(ctx context.Context, stateData *StateData, envelope msgEnvelope) error {
			mi := envelope.msgInfo
			if !envelope.fromReplay {
				if mi.PeerID != "" {
					err := wal.Write(mi)
					if err != nil {
						logger.Error("failed writing to WAL", "error", err)
					}
				} else {
					err := wal.WriteSync(mi) // NOTE: fsync
					if err != nil {
						panic(fmt.Errorf(
							"failed to write %v msg to consensus WAL due to %w; check your file system and restart the node",
							mi, err,
						))
					}
				}
			}
			return hd(ctx, stateData, envelope)
		}
	}
}

func loggingMiddleware(logger log.Logger) msgMiddlewareFunc {
	return func(hd msgHandlerFunc) msgHandlerFunc {
		return func(ctx context.Context, stateData *StateData, envelope msgEnvelope) error {
			args := append([]any{
				"height", stateData.Height,
				"round", stateData.Round,
				"peer", envelope.PeerID,
				"msg_type", fmt.Sprintf("%T", envelope.Msg),
			}, makeLogArgsFromMessage(envelope.Msg)...)
			loggerWithArgs := logger.With(args...)
			ctx = log.CtxWithLogger(ctx, loggerWithArgs)
			err := hd(ctx, stateData, envelope)
			if err != nil {
				// Downgrade only errors an unprivileged peer can trigger at
				// will (invalid signature, unexpected step) so a flood cannot
				// spam Error-level logs. Anything not positively matched —
				// including internal faults such as ErrPrivValidatorNotSet
				// surfaced while handling a peer message — stays at Error, so
				// this never hides a real problem.
				if isPeerFloodableError(err) {
					loggerWithArgs.Debug("rejected peer message", "error", err)
				} else {
					loggerWithArgs.Error("failed to process message", "error", err)
				}
				return nil
			}
			loggerWithArgs.Trace("message processed successfully")
			return nil
		}
	}
}

func msgInfoWithCtxMiddleware() msgMiddlewareFunc {
	return func(hd msgHandlerFunc) msgHandlerFunc {
		return func(ctx context.Context, stateData *StateData, envelope msgEnvelope) error {
			ctx = msgInfoWithCtx(ctx, envelope.msgInfo)
			return hd(ctx, stateData, envelope)
		}
	}
}

func makeLogArgsFromMessage(msg Message) []any {
	switch m := msg.(type) {
	case *ProposalMessage:
		return []any{
			"proposal_height", m.Proposal.Height,
			"proposal_round", m.Proposal.Round,
			"proposal_polRound", m.Proposal.POLRound,
		}
	case *BlockPartMessage:
		return []any{
			"block_height", m.Height,
			"block_round", m.Round,
			"part_index", m.Part.Index,
		}
	case *VoteMessage:
		return []any{
			"vote_type", m.Vote.Type.String(),
			"vote_height", m.Vote.Height,
			"vote_round", m.Vote.Round,
			"val_proTxHash", m.Vote.ValidatorProTxHash.ShortString(),
			"val_index", m.Vote.ValidatorIndex,
		}
	case *CommitMessage:
		return []any{
			"commit_height", m.Commit.Height,
			"commit_round", m.Commit.Round,
		}
	}
	panic("unsupported message type")
}
