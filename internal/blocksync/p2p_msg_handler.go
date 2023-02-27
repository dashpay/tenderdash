package blocksync

import (
	"context"
	"fmt"

	"github.com/gogo/protobuf/proto"

	"github.com/tendermint/tendermint/internal/p2p"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/libs/log"
	bcproto "github.com/tendermint/tendermint/proto/tendermint/blocksync"
	"github.com/tendermint/tendermint/types"
)

type (
	response               func(ctx context.Context, msg proto.Message) error
	blockP2PMessageHandler struct {
		logger    log.Logger
		store     sm.BlockStore
		peerAdder PeerAdder
	}
	recoveryP2PMessageHandler struct {
		logger log.Logger
		next   ConsumerHandler
	}
	loggerP2PMessageHandler struct {
		logger log.Logger
		next   ConsumerHandler
	}
)

func withRecoveryMiddleware(logger log.Logger) ConsumerMiddlewareFunc {
	hd := &recoveryP2PMessageHandler{logger: logger}
	return func(next ConsumerHandler) ConsumerHandler {
		hd.next = next
		return hd
	}
}

func withLoggerMiddleware(logger log.Logger) ConsumerMiddlewareFunc {
	hd := &loggerP2PMessageHandler{logger: logger}
	return func(next ConsumerHandler) ConsumerHandler {
		hd.next = next
		return hd
	}
}

func newBlockMessageHandler(logger log.Logger, store sm.BlockStore, peerAdder PeerAdder) *blockP2PMessageHandler {
	return &blockP2PMessageHandler{
		logger:    logger,
		store:     store,
		peerAdder: peerAdder,
	}
}

func consumerHandler(logger log.Logger, store sm.BlockStore, peerAdder PeerAdder) ConsumerHandler {
	return consumerHandlerWithMiddlewares(
		newBlockMessageHandler(logger, store, peerAdder),
		withLoggerMiddleware(logger),
		withRecoveryMiddleware(logger),
	)
}

// Handle handles a message from a block-sync message set
func (h *blockP2PMessageHandler) Handle(ctx context.Context, channel *Channel, envelope *p2p.Envelope) error {
	if envelope.ChannelID != BlockSyncChannel {
		return fmt.Errorf("unknown channel ID (%d) for envelope (%v)", envelope.ChannelID, envelope)
	}
	resp := responseFunc(channel, envelope.From)
	switch msg := envelope.Message.(type) {
	case *bcproto.BlockRequest:
		return h.handleBlockRequest(ctx, envelope, resp)
	case *bcproto.BlockResponse:
		return channel.Resolve(ctx, envelope)
	case *bcproto.StatusRequest:
		return resp(ctx, &bcproto.StatusResponse{
			Height: h.store.Height(),
			Base:   h.store.Base(),
		})
	case *bcproto.StatusResponse:
		h.peerAdder.AddPeer(newPeerData(envelope.From, msg.Base, msg.Height))
	case *bcproto.NoBlockResponse:
		h.logger.Debug("peer does not have the requested block",
			"peer", envelope.From,
			"height", msg.Height)
	default:
		return fmt.Errorf("received unknown message: %T", msg)
	}
	return nil
}

func (h *blockP2PMessageHandler) handleBlockRequest(ctx context.Context, envelope *p2p.Envelope, resp response) error {
	msg := envelope.Message.(*bcproto.BlockRequest)
	block := h.store.LoadBlock(msg.Height)
	if block == nil {
		h.logger.Info("peer requesting a block we do not have", "peer", envelope.From, "height", msg.Height)
		return resp(ctx, &bcproto.NoBlockResponse{Height: msg.Height})
	}
	commit := h.store.LoadSeenCommitAt(msg.Height)
	if commit == nil {
		return fmt.Errorf("found block in store with no commit: %v", block)
	}
	blockProto, err := block.ToProto()
	if err != nil {
		return fmt.Errorf("failed to convert block to protobuf: %w", err)
	}
	return resp(ctx, &bcproto.BlockResponse{
		Block:  blockProto,
		Commit: commit.ToProto(),
	})
}

// Handle recovers from panic if a panic happens
func (h *recoveryP2PMessageHandler) Handle(ctx context.Context, channel *Channel, envelope *p2p.Envelope) (err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("panic in processing message: %v", e)
			h.logger.Error(
				"recovering from processing message",
				"error", err,
			)
		}
	}()
	return h.next.Handle(ctx, channel, envelope)
}

// Handle writes an error message in a log
func (h *loggerP2PMessageHandler) Handle(ctx context.Context, channel *Channel, envelope *p2p.Envelope) error {
	err := h.next.Handle(ctx, channel, envelope)
	if err != nil {
		h.logger.Error("failed to handle a message from a p2p channel",
			"message_type", fmt.Sprintf("%T", envelope.Message),
			"error", err)
	}
	return nil
}

func responseFunc(channel *Channel, peerID types.NodeID) func(ctx context.Context, msg proto.Message) error {
	return func(ctx context.Context, msg proto.Message) error {
		return channel.Send(ctx, p2p.Envelope{
			To:      peerID,
			Message: msg,
		})
	}
}

func consumerHandlerWithMiddlewares(handler ConsumerHandler, mws ...ConsumerMiddlewareFunc) ConsumerHandler {
	for _, mw := range mws {
		handler = mw(handler)
	}
	return handler
}
