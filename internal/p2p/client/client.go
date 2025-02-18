//go:generate ../../../scripts/mockery_generate.sh BlockClient
//go:generate ../../../scripts/mockery_generate.sh SnapshotClient

package client

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cosmos/gogoproto/proto"
	"github.com/google/uuid"
	"github.com/hashicorp/go-multierror"
	"github.com/jonboulle/clockwork"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/promise"
	bcproto "github.com/dashpay/tenderdash/proto/tendermint/blocksync"
	protomem "github.com/dashpay/tenderdash/proto/tendermint/mempool"
	"github.com/dashpay/tenderdash/proto/tendermint/statesync"
	"github.com/dashpay/tenderdash/types"
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
	// SnapshotClient defines the methods which must be implemented by snapshot client
	SnapshotClient interface {
		// GetSnapshots requests a list of available snapshots from a peer without handling the response.
		// The snapshots will be sent by peer asynchronously and should be received by reading the channel separately.
		// The method returns an error if the request is not possible to send to the peer.
		GetSnapshots(ctx context.Context, peerID types.NodeID) error
		// GetChunk requests a snapshot chunk from a peer and returns a promise.Promise which will be resolved
		// as a response will be received or rejected by timeout, otherwise returns an error
		GetChunk(
			ctx context.Context,
			peerID types.NodeID,
			height uint64,
			format uint32,
			index uint32,
		) (*promise.Promise[*statesync.ChunkResponse], error)
		// GetParams requests a snapshot params from a peer.
		// The method returns a promise.Promise which will be resolved.
		GetParams(
			ctx context.Context,
			peerID types.NodeID,
			height uint64,
		) (*promise.Promise[*statesync.ParamsResponse], error)
		// GetLightBlock requests a light block from a peer.
		// The method returns a promise.Promise which will be resolved.
		GetLightBlock(
			ctx context.Context,
			peerID types.NodeID,
			height uint64,
		) (*promise.Promise[*statesync.LightBlockResponse], error)
	}
	// TxSender is the interface that wraps SendTxs method
	TxSender interface {
		// SendTxs sends a transaction to a peer
		SendTxs(ctx context.Context, peerID types.NodeID, tx types.Tx) error
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
		// rateLimit represents a rate limiter for the channel; can be nil
		rateLimit map[p2p.ChannelID]*RateLimit
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

// WithSendRateLimits defines a rate limiter for the provided channels.
//
// Provided rate limiter will be shared between provided channels.
// Use this function multiple times to set different rate limiters for different channels.
func WithSendRateLimits(rateLimit *RateLimit, channels ...p2p.ChannelID) OptionFunc {
	return func(c *Client) {
		for _, ch := range channels {
			c.rateLimit[ch] = rateLimit
		}
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
		rateLimit:      make(map[p2p.ChannelID]*RateLimit),
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
	msg := &bcproto.BlockRequest{Height: height}
	respCh, err := c.sendWithResponse(ctx, reqID, peerID, msg)
	if err != nil {
		return nil, err
	}
	return newPromise[*bcproto.BlockResponse](ctx, peerID, reqID, respCh, c), nil
}

// GetChunk requests a chunk from a peer and returns promise.Promise which resolve the result
func (c *Client) GetChunk(
	ctx context.Context,
	peerID types.NodeID,
	height uint64,
	version uint32,
	chunkID []byte,
) (*promise.Promise[*statesync.ChunkResponse], error) {
	reqID := uuid.NewString()
	msg := &statesync.ChunkRequest{Height: height, Version: version, ChunkId: chunkID}
	respCh, err := c.sendWithResponse(ctx, reqID, peerID, msg)
	if err != nil {
		return nil, err
	}
	return newPromise[*statesync.ChunkResponse](ctx, peerID, reqID, respCh, c), nil
}

// GetSnapshots requests snapshots from a peer
func (c *Client) GetSnapshots(ctx context.Context, peerID types.NodeID) error {
	return c.Send(ctx, p2p.Envelope{
		Attributes: map[string]string{RequestIDAttribute: uuid.NewString()},
		To:         peerID,
		Message:    &statesync.SnapshotsRequest{},
	})
}

// GetParams returns a promise.Promise which resolve the result if response received in time otherwise reject
func (c *Client) GetParams(
	ctx context.Context,
	peerID types.NodeID,
	height uint64,
) (*promise.Promise[*statesync.ParamsResponse], error) {
	reqID := uuid.NewString()
	msg := &statesync.ParamsRequest{Height: height}
	respCh, err := c.sendWithResponse(ctx, reqID, peerID, msg)
	if err != nil {
		return nil, err
	}
	return newPromise[*statesync.ParamsResponse](ctx, peerID, reqID, respCh, c), nil
}

// GetLightBlock returns a promise.Promise which resolve the result if response received in time otherwise reject
func (c *Client) GetLightBlock(
	ctx context.Context,
	peerID types.NodeID,
	height uint64,
) (*promise.Promise[*statesync.LightBlockResponse], error) {
	reqID := uuid.NewString()
	msg := &statesync.LightBlockRequest{Height: height}
	respCh, err := c.sendWithResponse(ctx, reqID, peerID, msg)
	if err != nil {
		return nil, err
	}
	return newPromise[*statesync.LightBlockResponse](ctx, peerID, reqID, respCh, c), nil
}

// GetSyncStatus requests a block synchronization status from all connected peers
// Since this is broadcast request, we can't use promise to process a response
// instead, we should be able to process the response as a normal message in the handler
func (c *Client) GetSyncStatus(ctx context.Context) error {
	reqID := uuid.NewString()
	return c.Send(ctx, p2p.Envelope{
		Attributes: map[string]string{RequestIDAttribute: reqID},
		Broadcast:  true,
		Message:    &bcproto.StatusRequest{},
	})
}

// SendTxs sends a transaction to the peer
func (c *Client) SendTxs(ctx context.Context, peerID types.NodeID, tx ...types.Tx) error {
	txs := make([][]byte, len(tx))
	for i := 0; i < len(tx); i++ {
		txs[i] = tx[i]
	}

	return c.Send(ctx, p2p.Envelope{
		To:      peerID,
		Message: &protomem.Txs{Txs: txs},
	})
}

// Send sends p2p message to a peer, allowed p2p.Envelope or p2p.PeerError types
func (c *Client) Send(ctx context.Context, msg any) error {
	return c.SendN(ctx, msg, 1)
}

// SendN sends p2p message to a peer, consuming `nTokens` from rate limiter.
//
// Allowed `msg` types are: p2p.Envelope or p2p.PeerError
func (c *Client) SendN(ctx context.Context, msg any, nTokens int) error {
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
		if limiter, ok := c.rateLimit[t.ChannelID]; ok {
			ok, err := limiter.Limit(ctx, t.To, nTokens)
			if err != nil {
				return fmt.Errorf("rate limited when sending message %T on channel %d to %s: %w",
					t.Message, t.ChannelID, t.To, err)
			}
			if !ok {
				c.logger.Debug("dropping message due to rate limit",
					"channel", t.ChannelID, "peer", t.To, "message", t.Message)
				return nil
			}
		}

		return ch.Send(ctx, t)
	}
	return fmt.Errorf("cannot send an unsupported message type %T", msg)
}

// Consume reads the messages from a p2p client and processes them using a consumer-handler
func (c *Client) Consume(ctx context.Context, params ConsumerParams) error {
	iter, err := c.chanStore.iter(ctx, params.ReadChannels...)
	if err != nil {
		c.logger.Error("failed to get a channel iterator", "error", err)
		return err
	}
	return c.iter(ctx, iter, params.Handler)
}

func (c *Client) iter(ctx context.Context, iter p2p.ChannelIterator, handler ConsumerHandler) error {
	for iter.Next(ctx) {
		envelope := iter.Envelope()
		if isMessageResolvable(envelope.Message) {
			err := c.resolve(ctx, envelope)
			if err != nil {
				c.logger.Error("failed to resolve response message", loggingArgsFromEnvelope(envelope)...)
				serr := c.Send(ctx, p2p.PeerError{NodeID: envelope.From, Err: err})
				if serr != nil {
					return multierror.Append(err, serr)
				}
			}
			continue
		}
		err := handler.Handle(ctx, c, envelope)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		if err != nil {
			c.logger.Error("failed to process message", loggingArgsFromEnvelope(envelope)...)
			serr := c.Send(ctx, p2p.PeerError{NodeID: envelope.From, Err: err})
			if serr != nil {
				return multierror.Append(err, serr)
			}
		}
	}
	return nil
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

func (c *Client) sendWithResponse(ctx context.Context, reqID string, peerID types.NodeID, msg proto.Message) (chan result, error) {
	err := c.Send(ctx, p2p.Envelope{
		Attributes: map[string]string{RequestIDAttribute: reqID},
		To:         peerID,
		Message:    msg,
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
	return c.addPending(reqID), nil
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
