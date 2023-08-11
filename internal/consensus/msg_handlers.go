package consensus

import (
	"context"
	"fmt"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/libs/log"
)

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

func newMsgInfoDispatcher(
	ctrl *Controller,
	proposaler cstypes.Proposaler,
	wal WALWriteFlusher,
	logger log.Logger,
) *msgInfoDispatcher {
	mws := []msgMiddlewareFunc{
		msgInfoWithCtxMiddleware(),
		loggingMiddleware(logger),
		walMiddleware(wal, logger),
	}
	proposalHandler := withMiddleware(proposalMessageHandler(proposaler), mws...)
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

func proposalMessageHandler(propSetter cstypes.ProposalSetter) msgHandlerFunc {
	return func(ctx context.Context, stateData *StateData, envelope msgEnvelope) error {
		msg := envelope.Msg.(*ProposalMessage)
		return propSetter.Set(msg.Proposal, envelope.ReceiveTime, &stateData.RoundState)
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
			logger.Debug("received block part from wrong round")
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
		err := ctrl.Dispatch(ctx, &AddVoteEvent{Vote: msg.Vote, PeerID: envelope.PeerID}, stateData)

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
		return ctrl.Dispatch(ctx, &TryAddCommitEvent{Commit: msg.Commit, PeerID: envelope.PeerID}, stateData)
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
				loggerWithArgs.Error("failed to process message", "error", err)
				return nil
			}
			loggerWithArgs.Debug("message processed successfully")
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

func logKeyValsWithError(keyVals []any, err error) []any {
	if err == nil {
		return keyVals
	}
	return append(keyVals, "error", err)
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
