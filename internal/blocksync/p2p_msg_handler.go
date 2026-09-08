package blocksync

import (
	"context"
	"fmt"

	"github.com/cosmos/gogoproto/proto"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/client"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/log"
	bcproto "github.com/dashpay/tenderdash/proto/tendermint/blocksync"
)

// maxPlausiblePeerHeight bounds a peer-reported height so that downstream
// height arithmetic (MaxHeight()+1, target counts) cannot overflow.
const maxPlausiblePeerHeight = int64(1) << 60

const (
	// Honest block sync issues one request every requestInterval and keeps at
	// most maxPendingRequestsPerPeer requests in flight to one peer. Allow headroom
	// for timer jitter while bounding peers that do not follow that schedule.
	blockRequestsPerSecond = 1000
	blockRequestBurst      = maxPendingRequestsPerPeer
)

type (
	response               func(ctx context.Context, msg proto.Message) error
	blockP2PMessageHandler struct {
		logger    log.Logger
		store     sm.BlockStore
		peerAdder PeerAdder
	}
)

func consumerHandler(
	ctx context.Context,
	logger log.Logger,
	store sm.BlockStore,
	peerAdder PeerAdder,
	rateLimitOptions ...client.RateLimitOptionFunc,
) client.ConsumerParams {
	requestCost := func(envelope *p2p.Envelope) uint {
		if _, ok := envelope.Message.(*bcproto.BlockRequest); ok {
			return 1
		}
		return 0
	}
	return client.ConsumerParams{
		ReadChannels: []p2p.ChannelID{p2p.BlockSyncChannel},
		Handler: client.HandlerWithMiddlewares(
			&blockP2PMessageHandler{
				logger:    logger,
				store:     store,
				peerAdder: peerAdder,
			},
			client.WithRecvRateLimitPerPeerHandlerWithBurst(
				ctx,
				blockRequestsPerSecond,
				blockRequestBurst,
				requestCost,
				true,
				logger,
				rateLimitOptions...,
			),
			client.WithValidateMessageHandler([]p2p.ChannelID{p2p.BlockSyncChannel}),
			client.WithErrorLoggerMiddleware(logger),
			client.WithRecoveryMiddleware(logger),
		),
	}
}

// Handle handles a message from a block-sync message set
func (h *blockP2PMessageHandler) Handle(ctx context.Context, p2pClient *client.Client, envelope *p2p.Envelope) error {
	resp := client.ResponseFuncFromEnvelope(p2pClient, envelope)
	switch msg := envelope.Message.(type) {
	case *bcproto.BlockRequest:
		return h.handleBlockRequest(ctx, envelope, resp)
	case *bcproto.StatusRequest:
		return resp(ctx, &bcproto.StatusResponse{
			Height: h.store.Height(),
			Base:   h.store.Base(),
		})
	case *bcproto.StatusResponse:
		if msg.Base < 0 || msg.Height < msg.Base || msg.Height > maxPlausiblePeerHeight {
			h.logger.Error("ignoring implausible peer status",
				"peer", envelope.From,
				"base", msg.Base,
				"height", msg.Height)
			return nil
		}
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
