package pex

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/libs/log"
)

type blockedReceiveChannel struct {
	p2p.Channel
	entered chan struct{}
	release chan struct{}
}

func (ch *blockedReceiveChannel) Receive(ctx context.Context) p2p.ChannelIterator {
	close(ch.entered)
	<-ctx.Done()
	<-ch.release
	return p2p.NewChannelIterator(make(chan p2p.Envelope))
}

func TestReactorWaitDrainsChannelReader(t *testing.T) {
	channel := &blockedReceiveChannel{entered: make(chan struct{}), release: make(chan struct{})}
	r := NewReactor(log.NewNopLogger(), nil,
		func(context.Context, *p2p.ChannelDescriptor) (p2p.Channel, error) { return channel, nil },
		func(context.Context, string) *p2p.PeerUpdates {
			return p2p.NewPeerUpdates(make(chan p2p.PeerUpdate), 1, "test")
		})
	require.NoError(t, r.Start(context.Background()))
	t.Cleanup(func() { r.Stop(); r.Wait() })
	defer close(channel.release)
	select {
	case <-channel.entered:
	case <-time.After(time.Second):
		t.Fatal("channel reader did not start")
	}
	r.Stop()
	done := make(chan struct{})
	go func() { r.Wait(); close(done) }()
	select {
	case <-done:
		t.Fatal("Wait returned before the channel reader finished")
	case <-time.After(30 * time.Millisecond):
	}
}
