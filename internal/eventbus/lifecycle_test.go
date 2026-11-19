package eventbus_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/eventbus"
	"github.com/dashpay/tenderdash/internal/pubsub"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

func TestWaitJoinsBlockedObserver(t *testing.T) {
	bus := eventbus.NewDefault(log.NewNopLogger())
	require.NoError(t, bus.Start(context.Background()))
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer func() { unblock(); bus.Stop(); bus.Wait() }()
	require.NoError(t, bus.Observe(context.Background(), func(pubsub.Message) error {
		close(entered)
		<-release
		return nil
	}))
	require.NoError(t, bus.PublishEventNewBlockHeader(types.EventDataNewBlockHeader{}))
	<-entered
	bus.Stop()
	done := make(chan struct{})
	go func() { bus.Wait(); close(done) }()
	select {
	case <-done:
		t.Fatal("Wait returned while the observer was still running")
	case <-time.After(20 * time.Millisecond):
	}
	unblock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Wait did not finish after the observer returned")
	}
}
