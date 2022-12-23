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

func (c *msgInfoDispatcher) dispatch(ctx context.Context, appState *AppState, msg Message, opts ...func(envelope *msgEnvelope)) error {
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
	return handler(ctx, appState, envelope)
}

func newMsgInfoDispatcher(
	behavior *Behavior,
	wal WALWriteFlusher,
	logger log.Logger,
	statsMsgQueue chan<- msgInfo,
) *msgInfoDispatcher {
	mws := []msgMiddlewareFunc{
		errorMiddleware(logger),
		walMiddleware(wal, logger),
	}
	proposalHandler := withMiddleware(proposalMessageHandler(behavior), mws...)
	blockPartHandler := withMiddleware(blockPartMessageHandler(behavior, statsMsgQueue), mws...)
	voteHandler := withMiddleware(voteMessageHandler(behavior, statsMsgQueue), mws...)
	commitHandler := withMiddleware(commitMessageHandler(behavior, statsMsgQueue), mws...)
	return &msgInfoDispatcher{
		proposalHandler:  proposalHandler,
		blockPartHandler: blockPartHandler,
		voteHandler:      voteHandler,
		commitHandler:    commitHandler,
	}
}

func proposalMessageHandler(b *Behavior) msgHandlerFunc {
	return func(ctx context.Context, appState *AppState, envelope msgEnvelope) error {
		msg := envelope.Msg.(*ProposalMessage)
		return b.SetProposal(ctx, appState, SetProposalEvent{
			Proposal: msg.Proposal,
			RecvTime: envelope.ReceiveTime,
		})
	}
}

func blockPartMessageHandler(b *Behavior, statsMsgQueue chan<- msgInfo) msgHandlerFunc {
	return func(ctx context.Context, appState *AppState, envelope msgEnvelope) error {
		msg := envelope.Msg.(*BlockPartMessage)

		// if the proposal is complete, we'll enterPrevote or tryFinalizeCommit
		added, err := b.AddProposalBlockPart(
			ctx,
			appState,
			AddProposalBlockPartEvent{Msg: msg, PeerID: envelope.PeerID, FromReplay: envelope.fromReplay},
		)

		if added {
			select {
			case statsMsgQueue <- envelope.msgInfo:
			case <-ctx.Done():
				return nil
			}
		}

		if err != nil && msg.Round != appState.Round {
			b.logger.Debug("received block part from wrong round",
				"height", appState.Height,
				"cs_round", appState.Round,
				"block_height", msg.Height,
				"block_round", msg.Round,
			)
			err = nil
		}

		b.logger.Debug(
			"received block part",
			"height", appState.Height,
			"round", appState.Round,
			"block_height", msg.Height,
			"block_round", msg.Round,
			"added", added,
			"peer", envelope.PeerID,
			"index", msg.Part.Index,
			"error", err,
		)
		return err
	}
}

func voteMessageHandler(b *Behavior, statsMsgQueue chan<- msgInfo) msgHandlerFunc {
	return func(ctx context.Context, appState *AppState, envelope msgEnvelope) error {
		msg := envelope.Msg.(*VoteMessage)
		// attempt to add the vote and dupeout the validator if its a duplicate signature
		// if the vote gives us a 2/3-any or 2/3-one, we transition
		added, err := b.TryAddVote(ctx, appState, TryAddVoteEvent{Vote: msg.Vote, PeerID: envelope.PeerID})
		if added {
			select {
			case statsMsgQueue <- envelope.msgInfo:
			case <-ctx.Done():
				return nil
			}
		}

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
		keyVals := []interface{}{
			"height", appState.Height,
			"cs_round", appState.Round,
			"vote_height", msg.Vote.Height,
			"vote_round", msg.Vote.Round,
			"added", added,
			"peer", envelope.PeerID,
		}
		if err != nil {
			keyVals = append(keyVals, "error", err)
		}
		b.logger.Debug("received vote", keyVals...)
		return nil
	}
}

func commitMessageHandler(b *Behavior, statsMsgQueue chan<- msgInfo) msgHandlerFunc {
	return func(ctx context.Context, appState *AppState, envelope msgEnvelope) error {
		msg := envelope.Msg.(*CommitMessage)
		// attempt to add the commit and dupeout the validator if its a duplicate signature
		// if the vote gives us a 2/3-any or 2/3-one, we transition
		added, err := b.TryAddCommit(
			ctx,
			appState,
			TryAddCommitEvent{
				Commit: msg.Commit,
				PeerID: envelope.PeerID,
			},
		)
		if added {
			statsMsgQueue <- envelope.msgInfo
		}
		b.logger.Debug(
			"received commit",
			"height", appState.Height,
			"cs_round", appState.Round,
			"commit_height", msg.Commit.Height,
			"commit_round", msg.Commit.Round,
			"added", added,
			"peer", envelope.PeerID,
			"error", err,
		)
		return nil
	}
}

func walMiddleware(wal WALWriteFlusher, logger log.Logger) msgMiddlewareFunc {
	return func(hd msgHandlerFunc) msgHandlerFunc {
		return func(ctx context.Context, appState *AppState, envelope msgEnvelope) error {
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
			return hd(ctx, appState, envelope)
		}
	}
}

func errorMiddleware(logger log.Logger) msgMiddlewareFunc {
	return func(hd msgHandlerFunc) msgHandlerFunc {
		return func(ctx context.Context, appState *AppState, envelope msgEnvelope) error {
			err := hd(ctx, appState, envelope)
			if err != nil {
				logger.Error(
					"failed to process message",
					"height", appState.Height,
					"round", appState.Round,
					"peer", envelope.PeerID,
					"msg_type", fmt.Sprintf("%T", envelope.Msg),
					"err", err,
				)
			}
			return nil
		}
	}
}
