package mempool

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/client"
	"github.com/dashpay/tenderdash/libs/log"
	protomem "github.com/dashpay/tenderdash/proto/tendermint/mempool"
	"github.com/dashpay/tenderdash/types"
)

type (
	mempoolP2PMessageHandler struct {
		logger  log.Logger
		checker TxChecker
		ids     *IDs
	}
)

func consumerHandler(logger log.Logger, checker TxChecker, ids *IDs) client.ConsumerParams {
	chanIDs := []p2p.ChannelID{p2p.MempoolChannel}
	return client.ConsumerParams{
		ReadChannels: chanIDs,
		Handler: client.HandlerWithMiddlewares(
			&mempoolP2PMessageHandler{
				logger:  logger,
				checker: checker,
				ids:     ids,
			},
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
		if err := h.checker.CheckTx(ctx, tx, nil, txInfo); err != nil {
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
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				// Do not propagate context
				// cancellation errors, but do
				// not continue to check
				// transactions from this
				// message if we are shutting down.
				return err
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
