package consensus

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
)

func TestScheduleTimeoutDoesNotBlockAfterShutdown(t *testing.T) {
	ticker := NewTimeoutTicker(log.NewNopLogger()).(*timeoutTicker)
	require.NoError(t, ticker.Start(context.Background()))
	ticker.Stop()
	ticker.Wait()
	for range cap(ticker.tickChan) {
		ticker.tickChan <- timeoutInfo{}
	}
	done := make(chan struct{})
	go func() { ticker.ScheduleTimeout(timeoutInfo{}); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout scheduling blocked after ticker shutdown")
	}
}
