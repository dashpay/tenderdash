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
		// tombstoneMtx guards tombstones
		tombstoneMtx sync.Mutex
		// tombstones lists retired request IDs in the order they were retired.
		// Every tombstone is given the same lifetime, so that order is also expiry
		// order and the entry due to expire first is always at the front. Forgetting
		// them is therefore a pop of the front prefix - one push and one pop per
		// request, O(1) amortized - rather than a scan of the whole map per request.
		tombstones []retiredRequest
	}
	// OptionFunc is a client optional function, it is used to override the default parameters in a Client
	OptionFunc func(c *Client)
	result     struct {
		Value any
		Err   error
	}
	// tombstone replaces the response channel of a request that timed out. Keeping
	// the request ID, rather than dropping it, is what lets resolveMessage tell a
	// late answer to a request we really made from a response quoting an ID we
	// never issued: the first is ordinary slowness, the second is unsolicited
	// traffic.
	//
	// It carries its own deadline so that resolveMessage can enforce the lifetime
	// on lookup. Sweeping reclaims memory, but correctness must not wait for it:
	// an idle client sweeps nothing, and a tombstone that outlives its window
	// would go on shielding a peer indefinitely.
	tombstone struct {
		expiresAt time.Time
	}
	// retiredRequest indexes one tombstone by the moment it stops being worth
	// remembering.
	retiredRequest struct {
		id        string
		expiresAt time.Time
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
	return newPromise[*bcproto.BlockResponse](ctx, reqID, respCh, c), nil
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
	return newPromise[*statesync.ChunkResponse](ctx, reqID, respCh, c), nil
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
	return newPromise[*statesync.ParamsResponse](ctx, reqID, respCh, c), nil
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
	return newPromise[*statesync.LightBlockResponse](ctx, reqID, respCh, c), nil
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

func (c *Client) resolveMessage(_ctx context.Context, respID string, res result) error {
	val, ok := c.pending.Load(respID)
	if !ok {
		return fmt.Errorf("pending response %s not found", respID)
	}
	switch v := val.(type) {
	case chan result:
		// The send must never block. Once the buffer holds an answer to this request
		// the promise takes that one, so anything further is a duplicate; and if the
		// promise has already settled, nothing will ever drain the buffer at all.
		// Waiting there would park the consumer goroutine - and with it every message
		// it carries - for as long as the node runs. A receiver never waits on a
		// channel whose buffer is non-empty, so a full buffer means nobody is owed
		// this value and dropping it loses nothing.
		//
		// There is deliberately no context arm here. With a default the select cannot
		// block, so there is nothing left to cancel, and an arm on an already-canceled
		// context would instead win a coin flip against delivering the response.
		select {
		case v <- res:
		default:
			c.logger.Debug("discarding a duplicate response", "response_id", respID)
		}
		return nil
	case tombstone:
		if c.clock.Now().After(v.expiresAt) {
			// Older than we promise to remember. An answer this late is indistinguishable
			// from one we never asked for, so it stops being shielded here.
			break
		}
		// We did issue this request, but stopped waiting for it. The peer is late,
		// which is not an offense, so the response is discarded and no error is
		// returned - an error here would make iter report the peer, which can evict
		// it outright and bypass the caller's own failure policy.
		c.logger.Debug("discarding a response for a timed-out request", "response_id", respID)
		return nil
	default:
		// Only a channel or a tombstone is ever stored, so reaching here means the
		// map grew a third value type. That is our bug, not the peer's: log it rather
		// than returning an error, which iter would turn into a PeerError against
		// whoever happened to answer.
		c.logger.Error("pending response has an unexpected type",
			"response_id", respID, "type", fmt.Sprintf("%T", val))
		return nil
	}
	return fmt.Errorf("pending response %s not found", respID)
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
	// Issuing requests is the only thing that grows the pending map, so it is also
	// the only place that has to shrink it. Sweeping inline keeps Client free of a
	// background goroutine that shutdown would then have to join, and it ties what
	// we retain to the rate at which we ourselves issue requests - a peer cannot
	// inflate it.
	c.expireTombstones()
	respCh := make(chan result, 1)
	c.pending.Store(reqID, respCh)
	return respCh
}

// retirePending drops a settled request from the pending map.
//
// Only a request that timed out leaves a tombstone behind, so that a peer which
// answers afterwards is recognized as late rather than as sending a response we
// never asked for. Every other ending - the answer arrived, or the caller went
// away - removes the entry outright. Tombstoning those too would also spare a
// duplicate response from being reported, but it would make what we retain scale
// with total throughput instead of with the requests actually in flight: a busy
// node completing thousands of requests a second would hold every one of their
// IDs for the whole window, where timeouts alone are capped by how many requests
// can be outstanding at once.
//
// The response channel is deliberately not closed in either case. Its only reader
// is the promise executor, which has already returned by the time this deferred
// call runs, so a close would signal nobody - while a resolver that loaded the
// channel a moment earlier would panic sending into it, on a path with no panic
// recovery above it. Leaving it open costs nothing, because resolveMessage never
// blocks on it: a late send either fills the one-slot buffer nobody reads or is
// dropped, and the channel becomes garbage as soon as it leaves the map.
func (c *Client) retirePending(reqID string, timedOut bool) {
	// Reclaim on retirement as well as on issuance. A node that stops making
	// requests would otherwise never run a sweep again and would hold its final
	// window of tombstones for as long as it lives.
	c.expireTombstones()
	if !timedOut {
		c.pending.Delete(reqID)
		return
	}
	c.tombstoneMtx.Lock()
	defer c.tombstoneMtx.Unlock()
	// Read the clock under the lock so the list stays sorted by expiry: concurrent
	// retirements would otherwise append in a different order than they timestamped.
	// Two request timeouts is long enough to still recognize a peer that answers
	// just after we gave up, and short enough to bound what we hold on to.
	expiresAt := c.clock.Now().Add(2 * c.reqTimeout)
	c.pending.Store(reqID, tombstone{expiresAt: expiresAt})
	c.tombstones = append(c.tombstones, retiredRequest{id: reqID, expiresAt: expiresAt})
}

// expireTombstones forgets retired request IDs whose lifetime has run out. A
// response later than that is indistinguishable from one we never asked for, so
// reporting the peer for it is fair.
func (c *Client) expireTombstones() {
	now := c.clock.Now()
	c.tombstoneMtx.Lock()
	defer c.tombstoneMtx.Unlock()
	expired := 0
	for expired < len(c.tombstones) && !c.tombstones[expired].expiresAt.After(now) {
		// Compare before deleting so only a tombstone is ever removed, never a live
		// request that somehow shares the ID.
		c.pending.CompareAndDelete(c.tombstones[expired].id, tombstone{expiresAt: c.tombstones[expired].expiresAt})
		expired++
	}
	c.tombstones = c.tombstones[expired:]
}

func (c *Client) timeout() <-chan time.Time {
	return c.clock.After(c.reqTimeout)
}

func newPromise[T proto.Message](
	ctx context.Context,
	reqID string,
	respCh chan result,
	client *Client,
) *promise.Promise[T] {
	return promise.New(func(resolve func(data T), reject func(err error)) {
		// Only a timeout leaves the request ID recognizable afterwards; see
		// retirePending for why the other endings must not.
		timedOut := false
		defer func() { client.retirePending(reqID, timedOut) }()
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
			// Reject and let the caller decide what a timeout is worth. Reporting
			// the peer here evicts it on a single slow request, which is a poor
			// signal: peers answer requests one at a time, so a busy peer times
			// out long before it is unhealthy, and evicting it also fails every
			// other request already in flight to it. Callers that want to act on
			// repeated timeouts can count them, and a genuinely dead connection
			// is still caught by the transport's ping/pong.
			timedOut = true
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
