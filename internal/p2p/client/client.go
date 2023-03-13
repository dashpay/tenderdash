//go:generate ../../../scripts/mockery_generate.sh BlockClient

package client

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/google/uuid"
	"github.com/hashicorp/go-multierror"
	"github.com/jonboulle/clockwork"

	"github.com/tendermint/tendermint/internal/p2p"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/libs/promise"
	bcproto "github.com/tendermint/tendermint/proto/tendermint/blocksync"
	"github.com/tendermint/tendermint/types"
)

// These attributes should use as a key in Envelope.Attributes map
const (
	// RequestIDAttribute is used to provide unique request-id value
	RequestIDAttribute = "RequestID"
	// ResponseIDAttribute is used to provide response-id that should be taken from received request-id
	ResponseIDAttribute = "ResponseID"
)

const peerTimeout = 15 * time.Second

var (
	ErrPeerNotResponded      = errors.New("peer did not send us anything")
	ErrCannotResolveResponse = errors.New("cannot resolve a result")
)

type (
	// Sender is the interface that wraps Send method
	Sender interface {
		Send(ctx context.Context, msg any) error
	}
	// BlockClient defines the methods which must be implemented by block client
	BlockClient interface {
		Sender
		// GetBlock is the method that requests a block by a specific height from a peer.
		// Since the request is asynchronous, then the method returns a promise that will be resolved
		// as a response will be received or rejected by timeout, otherwise returns an error
		GetBlock(ctx context.Context, height int64, peerID types.NodeID) (*promise.Promise[*bcproto.BlockResponse], error)
		// GetSyncStatus requests a block synchronization status from all connected peers
		GetSyncStatus(ctx context.Context) error
	}
	// Client is a stateful implementation of a client, which means that the client stores a request ID
	// in order to be able to resolve the response once it is received from the peer
	Client struct {
		chanStore      *chanStore
		clock          clockwork.Clock
		logger         log.Logger
		pending        sync.Map
		reqTimeout     time.Duration
		chanIDResolver func(msg proto.Message) p2p.ChannelID
	}
	// OptionFunc is a client optional function, it is used to override the default parameters in a Client
	OptionFunc func(c *Client)
	result     struct {
		Value any
		Err   error
	}
)

// WithLogger is an optional function to set logger to Client
func WithLogger(logger log.Logger) OptionFunc {
	return func(c *Client) {
		c.logger = logger
	}
}

// WithClock is an optional function to set clock to Client
func WithClock(clock clockwork.Clock) OptionFunc {
	return func(c *Client) {
		c.clock = clock
	}
}

// WithChanIDResolver is an option function to set channel ID resolver function
func WithChanIDResolver(resolver func(msg proto.Message) p2p.ChannelID) OptionFunc {
	return func(c *Client) {
		c.chanIDResolver = resolver
	}
}

// New creates and returns Client with optional functions
func New(descriptors map[p2p.ChannelID]*p2p.ChannelDescriptor, creator p2p.ChannelCreator, opts ...OptionFunc) *Client {
	client := &Client{
		chanStore:      newChanStore(descriptors, creator),
		clock:          clockwork.NewRealClock(),
		logger:         log.NewNopLogger(),
		reqTimeout:     peerTimeout,
		chanIDResolver: p2p.ResolveChannelID,
	}
	for _, opt := range opts {
		opt(client)
	}
	return client
}

// GetBlock requests a block from a peer and returns promise.Promise which resolve the result
// if response received in time otherwise reject
func (c *Client) GetBlock(ctx context.Context, height int64, peerID types.NodeID) (*promise.Promise[*bcproto.BlockResponse], error) {
	reqID := uuid.NewString()
	err := c.Send(ctx, p2p.Envelope{
		ChannelID:  p2p.BlockSyncChannel,
		Attributes: map[string]string{RequestIDAttribute: reqID},
		To:         peerID,
		Message:    &bcproto.BlockRequest{Height: height},
	})
	if err != nil {
		errSendError := c.Send(ctx, p2p.PeerError{
			NodeID: peerID,
			Err:    err,
		})
		if errSendError != nil {
			return nil, multierror.Append(err, errSendError)
		}
	}
	respCh := c.addPending(reqID)
	return newPromise[*bcproto.BlockResponse](ctx, peerID, reqID, respCh, c), nil
}

// GetSyncStatus requests a block synchronization status from all connected peers
// Since this is broadcast request, we can't use promise to process a response
// instead, we should be able to process the response as a normal message in the handler
func (c *Client) GetSyncStatus(ctx context.Context) error {
	reqID := uuid.NewString()
	return c.Send(ctx, p2p.Envelope{
		ChannelID:  p2p.BlockSyncChannel,
		Attributes: map[string]string{RequestIDAttribute: reqID},
		Broadcast:  true,
		Message:    &bcproto.StatusRequest{},
	})
}

// Send sends p2p message to a peer, allowed p2p.Envelope or p2p.PeerError types
func (c *Client) Send(ctx context.Context, msg any) error {
	switch t := msg.(type) {
	case p2p.PeerError:
		ch, err := c.chanStore.get(ctx, p2p.ErrorChannel)
		if err != nil {
			return err
		}
		return ch.SendError(ctx, t)
	case p2p.Envelope:
		if t.ChannelID == 0 {
			t.ChannelID = c.chanIDResolver(t.Message)
		}
		if _, ok := t.Attributes[RequestIDAttribute]; !ok {
			// populate RequestID if it is absent
			t.AddAttribute(RequestIDAttribute, uuid.NewString())
		}
		ch, err := c.chanStore.get(ctx, t.ChannelID)
		if err != nil {
			return err
		}
		return ch.Send(ctx, t)
	}
	return fmt.Errorf("cannot send an unsupported message type %T", msg)
}

// Consume reads the messages from a p2p client and processes them using a consumer-handler
func (c *Client) Consume(ctx context.Context, params ConsumerParams) {
	iter, err := c.chanStore.iter(ctx, params.ReadChannels...)
	if err != nil {
		c.logger.Error("failed to get a channel iterator", "error", err)
		return
	}
	c.iter(ctx, iter, params.Handler)
}

func (c *Client) iter(ctx context.Context, iter *p2p.ChannelIterator, handler ConsumerHandler) {
	for iter.Next(ctx) {
		envelope := iter.Envelope()
		if isMessageResolvable(envelope.Message) {
			err := c.resolve(ctx, envelope)
			if err != nil {
				c.logger.Error("failed to resolve response message", loggingArgsFromEnvelope(envelope)...)
				serr := c.Send(ctx, p2p.PeerError{NodeID: envelope.From, Err: err})
				if serr != nil {
					return
				}
			}
			continue
		}
		err := handler.Handle(ctx, c, envelope)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		if err != nil {
			c.logger.Error("failed to process message", loggingArgsFromEnvelope(envelope)...)
			serr := c.Send(ctx, p2p.PeerError{NodeID: envelope.From, Err: err})
			if serr != nil {
				return
			}
		}
	}
}

func (c *Client) resolve(ctx context.Context, envelope *p2p.Envelope) error {
	respID, ok := envelope.Attributes[ResponseIDAttribute]
	if !ok {
		return fmt.Errorf("responseID attribute is missed: %w", ErrCannotResolveResponse)
	}
	return c.resolveMessage(ctx, respID, result{Value: envelope.Message})
}

func (c *Client) resolveMessage(ctx context.Context, respID string, res result) error {
	val, ok := c.pending.Load(respID)
	if !ok {
		return fmt.Errorf("pending response %s not found", respID)
	}
	respCh := val.(chan result)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case respCh <- res:
	}
	return nil
}

func (c *Client) addPending(reqID string) chan result {
	respCh := make(chan result, 1)
	c.pending.Store(reqID, respCh)
	return respCh
}

func (c *Client) removePending(reqID string) {
	val, loaded := c.pending.LoadAndDelete(reqID)
	if !loaded {
		return
	}
	respCh := val.(chan result)
	close(respCh)
}

func (c *Client) timeout() <-chan time.Time {
	return c.clock.After(c.reqTimeout)
}

func newPromise[T proto.Message](
	ctx context.Context,
	peerID types.NodeID,
	reqID string,
	respCh chan result,
	client *Client,
) *promise.Promise[T] {
	return promise.New(func(resolve func(data T), reject func(err error)) {
		defer client.removePending(reqID)
		select {
		case <-ctx.Done():
			reject(fmt.Errorf("cannot complete a promise: %w", ctx.Err()))
			return
		case res := <-respCh:
			if res.Err != nil {
				reject(res.Err)
				return
			}
			resolve(res.Value.(T))
		case <-client.timeout():
			_ = client.Send(ctx, p2p.PeerError{
				NodeID: peerID,
				Err:    ErrPeerNotResponded,
			})
			reject(ErrPeerNotResponded)
		}
	})
}

func isMessageResolvable(msg proto.Message) bool {
	// This list should be expanded using other response messages
	switch msg.(type) {
	case *bcproto.BlockResponse:
		return true
	}
	return false
}

// ResponseFuncFromEnvelope creates a response function that is taken some parameters from received envelope
// to make the valid message that will be sent back to the peer
func ResponseFuncFromEnvelope(channel *Client, envelope *p2p.Envelope) func(ctx context.Context, msg proto.Message) error {
	return func(ctx context.Context, msg proto.Message) error {
		return channel.Send(ctx, p2p.Envelope{
			ChannelID: envelope.ChannelID,
			Attributes: map[string]string{
				ResponseIDAttribute: envelope.Attributes[RequestIDAttribute],
			},
			To:      envelope.From,
			Message: msg,
		})
	}
}

func loggingArgsFromEnvelope(envelope *p2p.Envelope, extraArgs ...any) []any {
	reqID := envelope.Attributes[RequestIDAttribute]
	args := make([]any, 0, 3+len(extraArgs))
	args = append(args, "ch_id", envelope.ChannelID, "request_id", reqID, "envelope", envelope)
	respID, ok := envelope.Attributes[ResponseIDAttribute]
	if ok {
		args = append(args, "response_id", respID)
	}
	return append(args, extraArgs...)
}
