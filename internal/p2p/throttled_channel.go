package p2p

import (
	"context"

	"golang.org/x/time/rate"

	"github.com/dashpay/tenderdash/libs/log"
)

// / Channel that will block if the send limit is reached
type ThrottledChannel struct {
	channel       Channel
	sendLimiter   *rate.Limiter
	recvLimiter   *rate.Limiter
	recvShouldErr bool

	logger log.Logger
}

// NewThrottledChannel creates a new throttled channel.
// The rate is specified in messages per second.
// The burst is specified in messages.
func NewThrottledChannel(channel Channel, sendLimit rate.Limit, sendBurst int,
	recvLimit rate.Limit, recvBurst int, recvShouldErr bool,
	logger log.Logger) *ThrottledChannel {

	var (
		sendLimiter *rate.Limiter
		recvLimiter *rate.Limiter
	)
	if sendLimit > 0 {
		sendLimiter = rate.NewLimiter(sendLimit, sendBurst)
	}
	if recvLimit > 0 {
		recvLimiter = rate.NewLimiter(recvLimit, recvBurst)
	}

	return &ThrottledChannel{
		channel:       channel,
		sendLimiter:   sendLimiter,
		recvLimiter:   recvLimiter,
		recvShouldErr: recvShouldErr,
		logger:        logger,
	}
}

var _ Channel = (*ThrottledChannel)(nil)

func (ch *ThrottledChannel) Send(ctx context.Context, envelope Envelope) error {
	// Wait until limiter allows us to proceed.
	if ch.sendLimiter != nil {
		if err := ch.sendLimiter.Wait(ctx); err != nil {
			return err
		}
	}

	return ch.channel.Send(ctx, envelope)
}

func (ch *ThrottledChannel) SendError(ctx context.Context, pe PeerError) error {
	// Wait until limiter allows us to proceed.
	if err := ch.sendLimiter.Wait(ctx); err != nil {
		return err
	}

	return ch.channel.SendError(ctx, pe)
}

func (ch *ThrottledChannel) Receive(ctx context.Context) ChannelIterator {
	if ch.recvLimiter == nil {
		return ch.channel.Receive(ctx)
	}

	innerIter := ch.channel.Receive(ctx)
	iter, err := ThrottledChannelIterator(ctx, ch.recvLimiter, innerIter, ch.recvShouldErr, ch.channel, ch.logger)
	if err != nil {
		ch.logger.Error("error creating ThrottledChannelIterator", "err", err)
		return nil
	}

	return iter
}

func (ch *ThrottledChannel) Err() error {
	return ch.channel.Err()
}

func (ch *ThrottledChannel) String() string {
	return ch.channel.String()
}
