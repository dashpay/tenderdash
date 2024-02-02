package p2p_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/dashpay/tenderdash/internal/p2p"
)

type mockChannel struct {
	counter atomic.Uint32
}

func (c *mockChannel) Len() int {
	return int(c.counter.Load())
}

func (c *mockChannel) Send(_ context.Context, _e p2p.Envelope) error {
	c.counter.Add(1)
	return nil
}

func (c *mockChannel) SendError(_ context.Context, _ p2p.PeerError) error {
	panic("not implemented")
}

func (c *mockChannel) Receive(_ context.Context) *p2p.ChannelIterator {
	panic("not implemented")
}

func (c *mockChannel) Err() error {
	panic("not implemented")
}

func (c *mockChannel) String() string {
	return "mock_channel"
}

func TestThrottledChannel(t *testing.T) {
	const n = 31
	const rate rate.Limit = 10
	const burst = int(rate)

	mock := &mockChannel{}

	ch := p2p.NewThrottledChannel(mock, rate, burst) // 1 message per second

	wg := sync.WaitGroup{}
	start := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			err := ch.Send(context.Background(), p2p.Envelope{})
			require.NoError(t, err)
			wg.Done()
		}()
	}

	time.Sleep(time.Second)
	assert.LessOrEqual(t, mock.Len(), burst+int(rate))

	// Wait until all finish

	wg.Wait()
	took := time.Since(start)
	assert.Equal(t, n, mock.Len())
	assert.GreaterOrEqual(t, took.Seconds(), 2.0)
}
