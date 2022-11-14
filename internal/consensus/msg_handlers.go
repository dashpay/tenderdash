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

func (c *msgInfoDispatcher) dispatch(ctx context.Context, msg Message, opts ...func(envelope *msgEnvelope)) error {
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
	return handler(ctx, envelope)
}

func newMsgInfoDispatcher(cs *State) *msgInfoDispatcher {
	mws := []msgMiddlewareFunc{
		errorMiddleware(cs, cs.logger),
		walMiddleware(&wrapWAL{getter: func() WALWriteFlusher { return cs.wal }}, cs.logger),
		globalLockMiddleware(cs),
	}
	proposalHandler := withMiddleware(proposalMessageHandler(cs), mws...)
	blockPartHandler := withMiddleware(blockPartMessageHandler(cs), mws...)
	voteHandler := withMiddleware(voteMessageHandler(cs), mws...)
	commitHandler := withMiddleware(commitMessageHandler(cs), mws...)
	return &msgInfoDispatcher{
		proposalHandler:  proposalHandler,
		blockPartHandler: blockPartHandler,
		voteHandler:      voteHandler,
		commitHandler:    commitHandler,
	}
}

func proposalMessageHandler(cs *State) msgHandlerFunc {
	return func(ctx context.Context, envelope msgEnvelope) error {
		msg := envelope.Msg.(*ProposalMessage)
		return cs.setProposal(msg.Proposal, envelope.ReceiveTime)
	}
}

func blockPartMessageHandler(cs *State) msgHandlerFunc {
	return func(ctx context.Context, envelope msgEnvelope) error {
		msg := envelope.Msg.(*BlockPartMessage)
		commitNotExist := cs.Commit == nil

		// if the proposal is complete, we'll enterPrevote or tryFinalizeCommit
		added, err := cs.addProposalBlockPart(ctx, msg, envelope.PeerID)

		if added && cs.ProposalBlockParts != nil && cs.ProposalBlockParts.IsComplete() && envelope.fromReplay {
			candidateState, err := cs.blockExec.ProcessProposal(ctx, cs.ProposalBlock, msg.Round, cs.state, true)
			if err != nil {
				panic(err)
			}
			cs.RoundState.CurrentRoundState = candidateState
		}

		// We unlock here to yield to any routines that need to read the the RoundState.
		// Previously, this code held the lock from the point at which the final block
		// part was received until the block executed against the application.
		// This prevented the reactor from being able to retrieve the most updated
		// version of the RoundState. The reactor needs the updated RoundState to
		// gossip the now completed block.
		//
		// This code can be further improved by either always operating on a copy
		// of RoundState and only locking when switching out State's copy of
		// RoundState with the updated copy or by emitting RoundState events in
		// more places for routines depending on it to listen for.
		cs.mtx.Unlock()

		cs.mtx.Lock()
		if added && commitNotExist && cs.ProposalBlockParts.IsComplete() {
			cs.handleCompleteProposal(ctx, msg.Height, envelope.fromReplay)
		}
		if added {
			select {
			case cs.statsMsgQueue <- envelope.msgInfo:
			case <-ctx.Done():
				return nil
			}
		}

		if err != nil && msg.Round != cs.Round {
			cs.logger.Debug("received block part from wrong round",
				"height", cs.Height,
				"cs_round", cs.Round,
				"block_height", msg.Height,
				"block_round", msg.Round,
			)
			err = nil
		}

		cs.logger.Debug(
			"received block part",
			"height", cs.Height,
			"round", cs.Round,
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

func voteMessageHandler(cs *State) msgHandlerFunc {
	return func(ctx context.Context, envelope msgEnvelope) error {
		msg := envelope.Msg.(*VoteMessage)
		// attempt to add the vote and dupeout the validator if its a duplicate signature
		// if the vote gives us a 2/3-any or 2/3-one, we transition
		added, err := cs.tryAddVote(ctx, msg.Vote, envelope.PeerID)
		if added {
			select {
			case cs.statsMsgQueue <- envelope.msgInfo:
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
			"height", cs.Height,
			"cs_round", cs.Round,
			"vote_height", msg.Vote.Height,
			"vote_round", msg.Vote.Round,
			"added", added,
			"peer", envelope.PeerID,
		}
		if err != nil {
			keyVals = append(keyVals, "error", err)
		}
		cs.logger.Debug("received vote", keyVals...)
		return nil
	}
}

func commitMessageHandler(cs *State) msgHandlerFunc {
	return func(ctx context.Context, envelope msgEnvelope) error {
		msg := envelope.Msg.(*CommitMessage)
		// attempt to add the commit and dupeout the validator if its a duplicate signature
		// if the vote gives us a 2/3-any or 2/3-one, we transition
		added, err := cs.tryAddCommit(ctx, msg.Commit, envelope.PeerID)
		if added {
			cs.statsMsgQueue <- envelope.msgInfo
		}
		cs.logger.Debug(
			"received commit",
			"height", cs.Height,
			"cs_round", cs.Round,
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
		return func(ctx context.Context, envelope msgEnvelope) error {
			mi := envelope.msgInfo
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
			return hd(ctx, envelope)
		}
	}
}

func errorMiddleware(cs *State, logger log.Logger) msgMiddlewareFunc {
	return func(hd msgHandlerFunc) msgHandlerFunc {
		return func(ctx context.Context, envelope msgEnvelope) error {
			err := hd(ctx, envelope)
			if err != nil {
				logger.Error(
					"failed to process message",
					"height", cs.Height,
					"round", cs.Round,
					"peer", envelope.PeerID,
					"msg_type", fmt.Sprintf("%T", envelope.Msg),
					"err", err,
				)
			}
			return nil
		}
	}
}

func globalLockMiddleware(cs *State) msgMiddlewareFunc {
	return func(hd msgHandlerFunc) msgHandlerFunc {
		return func(ctx context.Context, envelope msgEnvelope) error {
			cs.mtx.Lock()
			defer cs.mtx.Unlock()
			return hd(ctx, envelope)
		}
	}
}
