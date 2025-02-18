package consensus

import (
	"context"
	"errors"
	"fmt"

	"github.com/cosmos/gogoproto/proto"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/libs/log"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

var (
	errFailedLoadBlockPart = errors.New("failed to load block part")
)

type p2pMsgSender struct {
	logger log.Logger
	ps     *PeerState
	chans  channelBundle
}

func (c *p2pMsgSender) send(ctx context.Context, protoMsg proto.Message) error {
	switch pm := protoMsg.(type) {
	case *tmproto.Commit:
		return c.sendTo(ctx, c.chans.vote, &tmcons.Commit{Commit: pm})
	case *tmproto.Vote:
		return c.sendTo(ctx, c.chans.vote, &tmcons.Vote{Vote: pm})
	case *tmcons.BlockPart, *tmcons.ProposalPOL:
		return c.sendTo(ctx, c.chans.data, protoMsg)
	case *tmproto.Proposal:
		return c.sendTo(ctx, c.chans.data, &tmcons.Proposal{Proposal: *pm})
	case *tmcons.VoteSetMaj23, *tmcons.NewRoundStep:
		return c.sendTo(ctx, c.chans.state, protoMsg)
	}
	return fmt.Errorf("given unsupported p2p message %T", protoMsg)
}

func (c *p2pMsgSender) sendTo(ctx context.Context, ch p2p.Channel, msg proto.Message) error {
	c.logger.Trace("sending message", logKeyValsFromProto(msg, c.ps)...)
	select {
	case <-ctx.Done():
		return errReactorClosed
	default:
		return ch.Send(ctx, p2p.Envelope{
			To:      c.ps.peerID,
			Message: msg,
		})
	}
}

func logKeyValsFromProto(protoMsg proto.Message, ps *PeerState) []any {
	switch msg := protoMsg.(type) {
	case *tmcons.Commit:
		return []any{
			"message_type", "commit",
			"height", msg.Commit.Height,
			"round", msg.Commit.Round,
		}
	case *tmcons.Vote:
		psJSON, _ := ps.ToJSON()
		return []any{
			"message_type", "vote",
			"ps", psJSON,
			"val_proTxHash", types.ProTxHash(msg.Vote.ValidatorProTxHash).ShortString(),
			"height", msg.Vote.Height,
			"round", msg.Vote.Round,
			"size", msg.Vote.Size(),
			"vote", msg.Vote,
		}
	}
	return []any{}
}
