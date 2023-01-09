package consensus

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/benbjohnson/clock"

	"github.com/tendermint/tendermint/libs/log"
)

func TestPeerGossipWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	logger := log.NewTestingLogger(t)
	cfg := configSetup(t)
	fakeClock := clock.NewMock()

	handlerCalledCh := make(chan struct{}, 2)
	pg := peerGossipWorker{
		clock:  fakeClock,
		logger: logger,
		handlers: []gossipHandler{
			newGossipHandler(func(ctx context.Context, appState AppState) {
				handlerCalledCh <- struct{}{}
			}, 1*time.Second),
			newGossipHandler(func(ctx context.Context, appState AppState) {
				handlerCalledCh <- struct{}{}
			}, 1*time.Second),
		},
		running:       atomic.Bool{},
		appStateStore: NewAppStateStore(logger, cfg.Consensus),
		stopCh:        make(chan struct{}),
	}
	pg.start(ctx)
	for i := 0; i < 4; i++ {
		<-handlerCalledCh
	}
	defer cancel()
	pg.stop()
	close(handlerCalledCh)
}
