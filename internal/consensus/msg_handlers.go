package consensus

import (
	"context"
	"fmt"

	"github.com/tendermint/tendermint/libs/log"
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

func newMsgInfoDispatcher(ctrl *Controller, wal WALWriteFlusher, logger log.Logger) *msgInfoDispatcher {
	mws := []msgMiddlewareFunc{
		msgInfoWithCtxMiddleware(),
		errorMiddleware(logger),
		walMiddleware(wal, logger),
	}
	proposalHandler := withMiddleware(proposalMessageHandler(ctrl), mws...)
	blockPartHandler := withMiddleware(blockPartMessageHandler(ctrl, logger), mws...)
	voteHandler := withMiddleware(voteMessageHandler(ctrl, logger), mws...)
	commitHandler := withMiddleware(commitMessageHandler(ctrl, logger), mws...)
	return &msgInfoDispatcher{
		proposalHandler:  proposalHandler,
		blockPartHandler: blockPartHandler,
		voteHandler:      voteHandler,
		commitHandler:    commitHandler,
	}
}

func proposalMessageHandler(ctrl *Controller) msgHandlerFunc {
	return func(ctx context.Context, stateData *StateData, envelope msgEnvelope) error {
		msg := envelope.Msg.(*ProposalMessage)
		return ctrl.Dispatch(ctx, &SetProposalEvent{
			Proposal: msg.Proposal,
			RecvTime: envelope.ReceiveTime,
		}, stateData)
	}
}

func blockPartMessageHandler(ctrl *Controller, logger log.Logger) msgHandlerFunc {
	return func(ctx context.Context, stateData *StateData, envelope msgEnvelope) error {
		msg := envelope.Msg.(*BlockPartMessage)
		// if the proposal is complete, we'll enterPrevote or tryFinalizeCommit
		err := ctrl.Dispatch(ctx, &AddProposalBlockPartEvent{
			Msg:        msg,
			PeerID:     envelope.PeerID,
			FromReplay: envelope.fromReplay,
		}, stateData)

		if err != nil && msg.Round != stateData.Round {
			logger.Debug("received block part from wrong round",
				"height", stateData.Height,
				"cs_round", stateData.Round,
				"block_height", msg.Height,
				"block_round", msg.Round,
			)
			err = nil
		}
		logger.Debug(
			"received block part",
			"height", stateData.Height,
			"round", stateData.Round,
			"block_height", msg.Height,
			"block_round", msg.Round,
			"peer", envelope.PeerID,
			"index", msg.Part.Index,
			"error", err,
		)
		return err
	}
}

func voteMessageHandler(ctrl *Controller, logger log.Logger) msgHandlerFunc {
	return func(ctx context.Context, stateData *StateData, envelope msgEnvelope) error {
		msg := envelope.Msg.(*VoteMessage)
		logger = logger.With([]any{
			"height", stateData.Height,
			"cs_round", stateData.Round,
			"vote_type", msg.Vote.Type.String(),
			"vote_height", msg.Vote.Height,
			"vote_round", msg.Vote.Round,
			"val_proTxHash", msg.Vote.ValidatorProTxHash.ShortString(),
			"val_index", msg.Vote.ValidatorIndex,
			"quorum_hash", stateData.Validators.QuorumHash,
			"peer", envelope.PeerID,
		})
		ctx = ctxWithLogger(ctx, logger)
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
		logger.Debug("received vote")
		return nil
	}
}

func commitMessageHandler(ctrl *Controller, logger log.Logger) msgHandlerFunc {
	return func(ctx context.Context, stateData *StateData, envelope msgEnvelope) error {
		msg := envelope.Msg.(*CommitMessage)
		// attempt to add the commit and dupeout the validator if its a duplicate signature
		// if the vote gives us a 2/3-any or 2/3-one, we transition
		err := ctrl.Dispatch(ctx, &TryAddCommitEvent{Commit: msg.Commit, PeerID: envelope.PeerID}, stateData)
		logger.Debug(
			"received commit",
			"height", stateData.Height,
			"cs_round", stateData.Round,
			"commit_height", msg.Commit.Height,
			"commit_round", msg.Commit.Round,
			"peer", envelope.PeerID,
			"error", err,
		)
		return nil
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
						logger.Error("failed writing to WAL", "err", err)
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

func errorMiddleware(logger log.Logger) msgMiddlewareFunc {
	return func(hd msgHandlerFunc) msgHandlerFunc {
		return func(ctx context.Context, stateData *StateData, envelope msgEnvelope) error {
			err := hd(ctx, stateData, envelope)
			if err != nil {
				logger.Error(
					"failed to process message",
					"height", stateData.Height,
					"round", stateData.Round,
					"peer", envelope.PeerID,
					"msg_type", fmt.Sprintf("%T", envelope.Msg),
					"error", err,
				)
			}
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
