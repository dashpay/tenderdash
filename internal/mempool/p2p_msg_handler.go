package mempool

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/client"
	"github.com/dashpay/tenderdash/libs/log"
	protomem "github.com/dashpay/tenderdash/proto/tendermint/mempool"
	"github.com/dashpay/tenderdash/types"
)

type (
	mempoolP2PMessageHandler struct {
		logger  log.Logger
		config  *config.MempoolConfig
		checker TxChecker
		ids     *IDs
	}
)

func consumerHandler(ctx context.Context, logger log.Logger, config *config.MempoolConfig, checker TxChecker, ids *IDs) client.ConsumerParams {
	chanIDs := []p2p.ChannelID{p2p.MempoolChannel}

	nTokensFunc := func(e *p2p.Envelope) uint {
		if m, ok := e.Message.(*protomem.Txs); ok {
			return uint(len(m.Txs))
		}

		// unknown message type; this should not happen, we expect only Txs messages
		// But we don't panic, as this is not a critical error
		logger.Error("received unknown message type, expected Txs; assuming weight 1", "type", fmt.Sprintf("%T", e.Message), "from", e.From)
		return 1
	}

	return client.ConsumerParams{
		ReadChannels: chanIDs,
		Handler: client.HandlerWithMiddlewares(
			&mempoolP2PMessageHandler{
				logger:  logger,
				config:  config,
				checker: checker,
				ids:     ids,
			},
			client.WithRecvRateLimitPerPeerHandler(ctx, config.TxRecvRateLimit, nTokensFunc, true, logger),
			client.WithValidateMessageHandler(chanIDs),
			client.WithErrorLoggerMiddleware(logger),
			client.WithRecoveryMiddleware(logger),
		),
	}
}

// Handle handles a message from a block-sync message set
func (h *mempoolP2PMessageHandler) Handle(ctx context.Context, _ *client.Client, envelope *p2p.Envelope) error {
	logger := h.logger.With("peer", envelope.From)
	msg, ok := envelope.Message.(*protomem.Txs)
	if !ok {
		return fmt.Errorf("received unknown message: %T", msg)
	}
	protoTxs := msg.GetTxs()
	if len(protoTxs) == 0 {
		return errors.New("empty txs received from peer")
	}
	txInfo := TxInfo{
		SenderID:     h.ids.GetForPeer(envelope.From),
		SenderNodeID: envelope.From,
	}
	// some stats for logging
	start := time.Now()
	known := 0
	failed := 0
	for _, tx := range protoTxs {
		var (
			subCtx       context.Context
			subCtxCancel context.CancelFunc
		)
		if h.config.TimeoutCheckTx > 0 {
			subCtx, subCtxCancel = context.WithTimeout(ctx, h.config.TimeoutCheckTx)
		} else {
			subCtx, subCtxCancel = context.WithCancel(ctx)
		}

		defer subCtxCancel()

		if err := h.checker.CheckTx(subCtx, tx, nil, txInfo); err != nil {
			if errors.Is(err, types.ErrTxInCache) {
				// if the tx is in the cache,
				// then we've been gossiped a
				// Tx that we've already
				// got. Gossip should be
				// smarter, but it's not a
				// problem.
				known++
				continue
			}

			// In case of ctx cancelation, we return error as we are most likely shutting down.
			// Otherwise we just reject the tx.
			if errCtx := ctx.Err(); errCtx != nil {
				return errCtx
			}
			failed++
			logger.Error("checktx failed for tx",
				"tx", fmt.Sprintf("%X", types.Tx(tx).Hash()),
				"error", err)
		}
	}
	logger.Debug("processed txs from peer", "took", time.Since(start).String(),
		"num_txs", len(protoTxs),
		"already_known", known,
		"failed", failed,
	)
	return nil
}
