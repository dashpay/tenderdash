package client

import (
	"context"
	"errors"
	"fmt"

	"github.com/tendermint/tendermint/internal/p2p"
	"github.com/tendermint/tendermint/libs/log"
)

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
	ConsumerMiddlewareFunc    func(next ConsumerHandler) ConsumerHandler
	recoveryP2PMessageHandler struct {
		logger log.Logger
		next   ConsumerHandler
	}
	loggerP2PMessageHandler struct {
		logger log.Logger
		next   ConsumerHandler
	}
	validateMessageHandler struct {
		channelID p2p.ChannelID
		next      ConsumerHandler
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

// WithLoggerMiddleware creates error logging middleware
func WithLoggerMiddleware(logger log.Logger) ConsumerMiddlewareFunc {
	hd := &loggerP2PMessageHandler{logger: logger}
	return func(next ConsumerHandler) ConsumerHandler {
		hd.next = next
		return hd
	}
}

// WithValidateMessageHandler creates message validation middleware
func WithValidateMessageHandler(channelID p2p.ChannelID) ConsumerMiddlewareFunc {
	hd := &validateMessageHandler{
		channelID: channelID,
	}
	return func(next ConsumerHandler) ConsumerHandler {
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
			h.logger.Error(
				"recovering from processing message",
				"error", err,
			)
		}
	}()
	return h.next.Handle(ctx, client, envelope)
}

// Handle writes an error message in a log
func (h *loggerP2PMessageHandler) Handle(ctx context.Context, client *Client, envelope *p2p.Envelope) error {
	err := h.next.Handle(ctx, client, envelope)
	if err != nil {
		h.logger.Error("failed to handle a message from a p2p client",
			"message_type", fmt.Sprintf("%T", envelope.Message),
			"error", err)
	}
	return nil
}

// Handle validates is received envelope on required data
func (h *validateMessageHandler) Handle(ctx context.Context, client *Client, envelope *p2p.Envelope) error {
	if envelope.ChannelID != h.channelID {
		return fmt.Errorf("unknown channel ID (%d) for envelope (%v)", envelope.ChannelID, envelope)
	}
	_, ok := envelope.Attributes[RequestIDAttribute]
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
