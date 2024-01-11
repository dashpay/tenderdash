package p2p

import (
	"context"

	"golang.org/x/time/rate"
)

// / Channel that will block if the send limit is reached
type ThrottledChannel struct {
	channel Channel
	limiter *rate.Limiter
}

// NewThrottledChannel creates a new throttled channel.
// The rate is specified in messages per second.
// The burst is specified in messages.
//
// If unsure, use burst equal to 60 times the rate.
func NewThrottledChannel(channel Channel, r rate.Limit, burst int) *ThrottledChannel {
	return &ThrottledChannel{
		channel: channel,
		limiter: rate.NewLimiter(r, burst),
	}
}

var _ Channel = (*ThrottledChannel)(nil)

func (ch *ThrottledChannel) Send(ctx context.Context, envelope Envelope) error {
	// Wait until limiter allows us to proceed.
	if err := ch.limiter.Wait(ctx); err != nil {
		return err
	}

	return ch.channel.Send(ctx, envelope)
}

func (ch *ThrottledChannel) SendError(ctx context.Context, pe PeerError) error {
	// Wait until limiter allows us to proceed.
	if err := ch.limiter.Wait(ctx); err != nil {
		return err
	}

	return ch.channel.SendError(ctx, pe)
}

func (ch *ThrottledChannel) Receive(ctx context.Context) *ChannelIterator {
	return ch.channel.Receive(ctx)
}

func (ch *ThrottledChannel) Err() error {
	return ch.channel.Err()
}

func (ch *ThrottledChannel) String() string {
	return ch.channel.String()
}
