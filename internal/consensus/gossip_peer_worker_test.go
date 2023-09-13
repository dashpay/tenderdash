package consensus

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
)

func TestPeerGossipWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	logger := log.NewTestingLogger(t)
	cfg := configSetup(t)
	fakeClock := clockwork.NewFakeClock()
	emitter := eventemitter.New()

	handlerCalledCh := make(chan struct{}, 2)
	pg := peerGossipWorker{
		clock:  fakeClock,
		logger: logger,
		handlers: []gossipHandler{
			newGossipHandler(func(ctx context.Context, appState StateData) {
				handlerCalledCh <- struct{}{}
			}, 1*time.Second),
			newGossipHandler(func(ctx context.Context, appState StateData) {
				handlerCalledCh <- struct{}{}
			}, 1*time.Second),
		},
		running:        atomic.Bool{},
		stateDataStore: NewStateDataStore(NopMetrics(), logger, cfg.Consensus, emitter),
		stopCh:         make(chan struct{}),
	}
	require.False(t, pg.IsRunning())
	err := pg.Start(ctx)
	require.NoError(t, err)
	require.True(t, pg.IsRunning())
	for i := 0; i < 4; i++ {
		<-handlerCalledCh
	}
	defer cancel()
	pg.Stop()
	require.False(t, pg.IsRunning())
	close(handlerCalledCh)
}
