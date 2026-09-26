package indexer_test

import (
	"context"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/eventbus"
	"github.com/dashpay/tenderdash/internal/pubsub"
	"github.com/dashpay/tenderdash/internal/state/indexer"
	"github.com/dashpay/tenderdash/internal/state/indexer/sink/kv"
	"github.com/dashpay/tenderdash/libs/log"
)

func TestIndexerStopsWhileEventBusRemainsLive(t *testing.T) {
	bus := eventbus.NewDefault(log.NewNopLogger())
	require.NoError(t, bus.Start(context.Background()))
	t.Cleanup(func() { bus.Stop(); bus.Wait() })
	svc := indexer.NewService(indexer.ServiceArgs{
		Logger: log.NewNopLogger(), EventBus: bus,
		Sinks: []indexer.EventSink{kv.NewEventSink(dbm.NewMemDB())},
	})
	require.NoError(t, svc.Start(context.Background()))
	svc.Stop()
	done := make(chan struct{})
	go func() { svc.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("indexer shutdown waited for the live event bus")
	}
	require.True(t, bus.IsRunning())
	require.NoError(t, bus.Observe(context.Background(), func(_ pubsub.Message) error { return nil }))
}
