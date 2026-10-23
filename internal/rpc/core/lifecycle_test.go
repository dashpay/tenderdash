package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/eventbus"
	"github.com/dashpay/tenderdash/internal/eventlog"
	"github.com/dashpay/tenderdash/internal/pubsub"
	"github.com/dashpay/tenderdash/internal/pubsub/query"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

type blockedLogEvent struct {
	types.EventDataNewBlockHeader
	entered chan struct{}
	release chan struct{}
}

func (e blockedLogEvent) ABCIEvents() []abci.Event {
	close(e.entered)
	<-e.release
	return nil
}

func TestRPCServiceRetryAndEventLogDrain(t *testing.T) {
	logger := log.NewNopLogger()
	bus := eventbus.NewDefault(logger)
	require.NoError(t, bus.Start(context.Background()))
	defer func() { bus.Stop(); bus.Wait() }()
	eventLog, err := eventlog.New(eventlog.LogSettings{WindowSize: time.Minute})
	require.NoError(t, err)
	env := &Environment{Logger: logger, EventBus: bus, EventLog: eventLog}
	conf := config.DefaultConfig()
	// A failed subscription must leave the RPC owner reusable.
	_, err = bus.SubscribeWithArgs(context.Background(), pubsub.SubscribeArgs{ClientID: "event-log-subscriber", Query: query.All})
	require.NoError(t, err)
	require.Error(t, env.StartService(context.Background(), conf, nil))
	require.NoError(t, bus.UnsubscribeAll(context.Background(), "event-log-subscriber"))
	require.NoError(t, env.StartService(context.Background(), conf, nil))
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	require.NoError(t, bus.Publish(types.EventNewBlockHeaderValue, blockedLogEvent{entered: entered, release: release}))
	<-entered
	go func() { env.StopService(); close(done) }()
	select {
	case <-done:
		close(release)
		t.Fatal("RPC shutdown returned while event-log forwarding was active")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	<-done
	require.Zero(t, bus.NumClientSubscriptions("event-log-subscriber"))
}
