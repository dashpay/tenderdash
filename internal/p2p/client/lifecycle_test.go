package client

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
)

type blockedGCClock struct {
	clockwork.Clock
	entered chan struct{}
	release chan struct{}
}

func (c *blockedGCClock) Now() time.Time {
	close(c.entered)
	<-c.release
	return c.Clock.Now()
}

func TestRateLimitCloseJoinsGC(t *testing.T) {
	fake := clockwork.NewFakeClock()
	clock := &blockedGCClock{Clock: fake, entered: make(chan struct{}), release: make(chan struct{})}
	limiter := NewRateLimit(context.Background(), 1, true, log.NewNopLogger(), WithRateLimitClock(clock))
	t.Cleanup(limiter.Close)
	defer close(clock.release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, fake.BlockUntilContext(ctx, 1))
	fake.Advance(PeerRateLimitLifetime * time.Second)
	select {
	case <-clock.entered:
	case <-time.After(time.Second):
		t.Fatal("garbage collection did not start")
	}
	done := make(chan struct{})
	go func() { limiter.Close(); close(done) }()
	select {
	case <-done:
		t.Fatal("Close returned before garbage collection finished")
	case <-time.After(30 * time.Millisecond):
	}
}
