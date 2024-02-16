package p2p_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/libs/log"
)

type mockChannel struct {
	sent     atomic.Uint32
	received atomic.Uint32
	errored  atomic.Uint32
}

func (c *mockChannel) SendCount() int {
	return int(c.sent.Load())
}

func (c *mockChannel) RecvCount() int {
	return int(c.received.Load())
}

func (c *mockChannel) Send(_ context.Context, _e p2p.Envelope) error {
	c.sent.Add(1)
	return nil
}

func (c *mockChannel) SendError(_ context.Context, _ p2p.PeerError) error {
	c.errored.Add(1)
	return nil
}

func (c *mockChannel) Receive(ctx context.Context) p2p.ChannelIterator {
	var pipe = make(chan p2p.Envelope, 5)

	go func() {
		for {
			select {
			case pipe <- p2p.Envelope{}:
				c.received.Add(1)
			case <-ctx.Done():
				close(pipe)
				return
			}
		}
	}()

	return p2p.NewChannelIterator(pipe)
}

func (c *mockChannel) Err() error {
	if e := c.errored.Load(); e > 0 {
		return fmt.Errorf("mock_channel_error: error count: %d", e)
	}
	return nil
}

func (c *mockChannel) String() string {
	return "mock_channel"
}

func TestThrottledChannelSend(t *testing.T) {
	const n = 31
	const rate rate.Limit = 10
	const burst = int(rate)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	logger := log.NewTestingLogger(t)

	mock := &mockChannel{}

	ch := p2p.NewThrottledChannel(mock, rate, burst, 0, 0, false, logger) // 1 message per second

	wg := sync.WaitGroup{}
	start := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			err := ch.Send(ctx, p2p.Envelope{})
			require.NoError(t, err)
			wg.Done()
		}()
	}

	time.Sleep(time.Second)
	assert.LessOrEqual(t, mock.SendCount(), burst+int(rate))

	// Wait until all finish
	wg.Wait()
	took := time.Since(start)
	assert.Equal(t, n, mock.SendCount())
	assert.GreaterOrEqual(t, took.Seconds(), 2.0)
}

// Given some thrrottled channel that generates messages all the time, we should error out after receiving a rate
// of 10 messages per second.
func TestThrottledChannelRecvError(t *testing.T) {
	const rate rate.Limit = 10
	const burst = int(rate)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	logger := log.NewTestingLogger(t)

	mock := &mockChannel{}

	ch := p2p.NewThrottledChannel(mock, rate, burst, rate, burst, true, logger) // 1 message per second

	start := time.Now()

	assert.NoError(t, mock.Err())

	iter := ch.Receive(ctx)

	for i := 0; i < burst+int(rate)+1; i++ {
		assert.True(t, iter.Next(ctx))

		e := iter.Envelope()
		if e == nil {
			t.Error("nil envelope")
		}
	}

	err := mock.Err()
	t.Logf("expected mock error: %v", err)
	assert.Error(t, mock.Err())

	// Wait until all finish
	cancel()

	took := time.Since(start)
	assert.GreaterOrEqual(t, took.Seconds(), 1.0)
}

// Given some thrrottled channel that generates messages all the time, we should be able to receive them at a rate
// of 10 messages per second.
func TestThrottledChannelRecv(t *testing.T) {
	const n = 31
	const rate rate.Limit = 10
	const burst = int(rate)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	logger := log.NewTestingLogger(t)

	mock := &mockChannel{}

	ch := p2p.NewThrottledChannel(mock, rate, burst, rate, burst, false, logger) // 1 message per second

	start := time.Now()
	count := 0
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		iter := ch.Receive(ctx)
		for iter.Next(ctx) {
			e := iter.Envelope()
			if e == nil {
				t.Error("nil envelope")
			}
			count++
		}
		wg.Done()
	}()

	time.Sleep(time.Second)
	assert.LessOrEqual(t, mock.SendCount(), burst+int(rate))

	// Wait until all finish
	cancel()
	wg.Wait()

	took := time.Since(start)
	assert.Greater(t, mock.RecvCount(), count*100, "we should generate much more messages than we can receive")
	assert.GreaterOrEqual(t, took.Seconds(), 1.0)
}
