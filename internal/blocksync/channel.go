//go:generate ../../scripts/mockery_generate.sh BlockClient

package blocksync

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/benbjohnson/clock"
	"github.com/hashicorp/go-multierror"

	"github.com/tendermint/tendermint/internal/p2p"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/libs/promise"
	bcproto "github.com/tendermint/tendermint/proto/tendermint/blocksync"
	"github.com/tendermint/tendermint/types"
)

type (
	// BlockClient ...
	BlockClient interface {
		GetBlock(ctx context.Context, height int64, peerID types.NodeID) (*promise.Promise[*bcproto.BlockResponse], error)
		Send(ctx context.Context, msg any) error
	}
	// Channel ...
	Channel struct {
		mtx     sync.Mutex
		channel p2p.Channel
		clock   clock.Clock
		logger  log.Logger
		//pending map[string]chan result
		pending sync.Map
		timeout time.Duration
	}
	// ChannelOptionFunc ...
	ChannelOptionFunc func(c *Channel)
	result            struct {
		Value any
		Err   error
	}
)

// ChannelWithLogger ...
func ChannelWithLogger(logger log.Logger) ChannelOptionFunc {
	return func(c *Channel) {
		c.logger = logger
	}
}

// ChannelWithClock ...
func ChannelWithClock(clock clock.Clock) ChannelOptionFunc {
	return func(c *Channel) {
		c.clock = clock
	}
}

// NewChannel ...
func NewChannel(ch p2p.Channel, opts ...ChannelOptionFunc) *Channel {
	channel := &Channel{
		channel: ch,
		clock:   clock.New(),
		logger:  log.NewNopLogger(),
		timeout: peerTimeout,
	}
	for _, opt := range opts {
		opt(channel)
	}
	return channel
}

// GetBlock ...
func (c *Channel) GetBlock(ctx context.Context, height int64, peerID types.NodeID) (*promise.Promise[*bcproto.BlockResponse], error) {
	err := c.Send(ctx, p2p.Envelope{
		To:      peerID,
		Message: &bcproto.BlockRequest{Height: height},
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
	reqID := makeGetBlockReqID(height, peerID)
	respCh := c.addPending(reqID)
	p := promise.New(func(resolve func(data *bcproto.BlockResponse), reject func(err error)) {
		select {
		case <-ctx.Done():
			return
		case res := <-respCh:
			if res.Err != nil {
				reject(res.Err)
				return
			}
			resolve(res.Value.(*bcproto.BlockResponse))
		case <-c.clock.After(c.timeout):
			_ = c.Send(ctx, p2p.PeerError{
				NodeID: peerID,
				Err:    errPeerNotResponded,
			})
			c.logger.Error("SendTimeout", "reason", errPeerNotResponded, "timeout", peerTimeout)
			reject(errPeerNotResponded)
		}
		c.pending.Delete(reqID)
		close(respCh)
	})
	return p, nil
}

// Send sends p2p message to a peer, allowed p2p.Envelope or p2p.PeerError types
func (c *Channel) Send(ctx context.Context, msg any) error {
	switch t := msg.(type) {
	case p2p.PeerError:
		return c.channel.SendError(ctx, t)
	case p2p.Envelope:
		return c.channel.Send(ctx, t)
	}
	return fmt.Errorf("unsupported message type %T", msg)
}

// Resolve ...
func (c *Channel) Resolve(ctx context.Context, envelope *p2p.Envelope) error {
	switch msg := envelope.Message.(type) {
	case *bcproto.BlockResponse:
		reqID := makeGetBlockReqID(msg.Commit.Height, envelope.From)
		return c.resolveResponse(ctx, reqID, result{Value: msg})
	default:
		return fmt.Errorf("cannot resolve response to unknown message: %T", msg)
	}
}

func (c *Channel) resolveResponse(ctx context.Context, reqID string, res result) error {
	val, ok := c.pending.Load(reqID)
	if !ok {
		return fmt.Errorf("cannot resolve a result for a request %s", reqID)
	}
	respCh := val.(chan result)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case respCh <- res:
	}
	return nil
}

func (c *Channel) addPending(reqID string) chan result {
	respCh := make(chan result, 1)
	c.pending.Store(reqID, respCh)
	return respCh
}

func makeGetBlockReqID(height int64, peerID types.NodeID) string {
	heightStr := strconv.FormatInt(height, 10)
	return string(peerID) + heightStr
}
