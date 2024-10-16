package client

import (
	"context"
	"errors"
	"fmt"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/math"
)

// DefaultRecvBurstMultiplier tells how many times burst is bigger than the limit in recvRateLimitPerPeerHandler
const DefaultRecvBurstMultiplier = 10

var (
	ErrRequestIDAttributeRequired  = errors.New("envelope requestID attribute is required")
	ErrResponseIDAttributeRequired = errors.New("envelope responseID attribute is required")
)

type (
	// ConsumerHandler is the interface that wraps a Handler method.
	// This interface must be implemented by the p2p message handler
	// and must be used in conjunction with the p2p consumer.
	ConsumerHandler interface {
		Handle(ctx context.Context, client *Client, envelope *p2p.Envelope) error
	}
	// ConsumerMiddlewareFunc is used to wrap ConsumerHandler to provide the ability to do something
	// before or after the handler execution
	ConsumerMiddlewareFunc func(next ConsumerHandler) ConsumerHandler
	// ConsumerParams is p2p handler parameters set
	ConsumerParams struct {
		ReadChannels []p2p.ChannelID
		Handler      ConsumerHandler
	}
	recoveryP2PMessageHandler struct {
		logger log.Logger
		next   ConsumerHandler
	}
	errorLoggerP2PMessageHandler struct {
		logger log.Logger
		next   ConsumerHandler
	}
	validateMessageHandler struct {
		allowedChannelIDs map[p2p.ChannelID]struct{}
		next              ConsumerHandler
	}

	// TokenNumberFunc is a function that returns number of tokens to consume for a given envelope
	TokenNumberFunc func(*p2p.Envelope) uint

	recvRateLimitPerPeerHandler struct {
		RateLimit

		// next is the next handler in the chain
		next ConsumerHandler

		// nTokens is a function that returns number of tokens to consume for a given envelope; if unsure, return 1
		nTokensFunc TokenNumberFunc
	}
)

// WithRecoveryMiddleware creates panic recovery middleware
func WithRecoveryMiddleware(logger log.Logger) ConsumerMiddlewareFunc {
	hd := &recoveryP2PMessageHandler{logger: logger}
	return func(next ConsumerHandler) ConsumerHandler {
		hd.next = next
		return hd
	}
}

// WithErrorLoggerMiddleware creates error logging middleware
func WithErrorLoggerMiddleware(logger log.Logger) ConsumerMiddlewareFunc {
	hd := &errorLoggerP2PMessageHandler{logger: logger}
	return func(next ConsumerHandler) ConsumerHandler {
		hd.next = next
		return hd
	}
}

// WithValidateMessageHandler creates message validation middleware
func WithValidateMessageHandler(allowedChannelIDs []p2p.ChannelID) ConsumerMiddlewareFunc {
	hd := &validateMessageHandler{
		allowedChannelIDs: map[p2p.ChannelID]struct{}{},
	}
	for _, chanID := range allowedChannelIDs {
		hd.allowedChannelIDs[chanID] = struct{}{}
	}
	return func(next ConsumerHandler) ConsumerHandler {
		hd.next = next
		return hd
	}
}

func WithRecvRateLimitPerPeerHandler(ctx context.Context, limit float64, nTokensFunc TokenNumberFunc, drop bool, logger log.Logger) ConsumerMiddlewareFunc {
	return func(next ConsumerHandler) ConsumerHandler {
		hd := &recvRateLimitPerPeerHandler{
			RateLimit:   *NewRateLimit(ctx, limit, drop, logger),
			nTokensFunc: nTokensFunc,
		}

		hd.next = next
		return hd
	}
}

// HandlerWithMiddlewares is a function that wraps a handler in middlewares
func HandlerWithMiddlewares(handler ConsumerHandler, mws ...ConsumerMiddlewareFunc) ConsumerHandler {
	for _, mw := range mws {
		handler = mw(handler)
	}
	return handler
}

// Handle recovers from panic if a panic happens
func (h *recoveryP2PMessageHandler) Handle(ctx context.Context, client *Client, envelope *p2p.Envelope) (err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("panic in processing message: %v", e)
			h.logger.Error("recovering from processing message", "error", err)
		}
	}()
	return h.next.Handle(ctx, client, envelope)
}

// Handle writes an error message in a log
func (h *errorLoggerP2PMessageHandler) Handle(ctx context.Context, client *Client, envelope *p2p.Envelope) error {
	err := h.next.Handle(ctx, client, envelope)
	if err != nil {
		reqID := envelope.Attributes[RequestIDAttribute]
		h.logger.Error("failed to handle a message from a p2p client",
			"message_type", fmt.Sprintf("%T", envelope.Message),
			"request_id", reqID,
			"error", err)
	}
	return err
}

// Handle validates is received envelope on required data
func (h *validateMessageHandler) Handle(ctx context.Context, client *Client, envelope *p2p.Envelope) error {
	_, ok := h.allowedChannelIDs[envelope.ChannelID]
	if !ok {
		return fmt.Errorf("unknown channel ID (%d) for envelope (%v)", envelope.ChannelID, envelope)
	}
	_, ok = envelope.Attributes[RequestIDAttribute]
	if !ok {
		return ErrRequestIDAttributeRequired
	}
	if isMessageResolvable(envelope.Message) {
		_, ok = envelope.Attributes[ResponseIDAttribute]
		if !ok {
			return ErrResponseIDAttributeRequired
		}
	}
	return h.next.Handle(ctx, client, envelope)
}

func (h *recvRateLimitPerPeerHandler) Handle(ctx context.Context, client *Client, envelope *p2p.Envelope) error {
	accepted, err := h.RateLimit.Limit(ctx, envelope.From, math.MustConvert[uint, int](h.nTokensFunc(envelope)))
	if err != nil {
		return fmt.Errorf("rate limit failed for peer '%s;: %w", envelope.From, err)
	}
	if !accepted {
		h.logger.Debug("silently dropping message due to rate limit", "peer", envelope.From, "envelope", envelope)
		return nil
	}

	return h.next.Handle(ctx, client, envelope)
}
