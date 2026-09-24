package consensus

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/service"
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
			newGossipHandler(func(_ctx context.Context, _appState StateData) {
				handlerCalledCh <- struct{}{}
			}, 1*time.Second),
			newGossipHandler(func(_ctx context.Context, _appState StateData) {
				handlerCalledCh <- struct{}{}
			}, 1*time.Second),
		},
		stateDataStore: NewStateDataStore(NopMetrics(), logger, cfg.Consensus, emitter),
	}
	pg.BaseService = *service.NewBaseService(logger, "PeerGossipWorker", &pg)
	require.False(t, pg.IsRunning())
	err := pg.Start(ctx)
	require.NoError(t, err)
	require.True(t, pg.IsRunning())
	for i := 0; i < 4; i++ {
		<-handlerCalledCh
	}
	defer cancel()
	pg.Stop()
	pg.Wait()
	require.False(t, pg.IsRunning())
	close(handlerCalledCh)
}

func TestPeerGossipWorkerWaitDrainsHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	cs, _ := makeState(ctx, t, makeStateArgs{})
	pg := &peerGossipWorker{
		clock: clockwork.NewRealClock(), logger: log.NewNopLogger(), stateDataStore: cs.stateDataStore,
		handlers: []gossipHandler{newGossipHandler(func(context.Context, StateData) { close(entered); <-release }, time.Hour)},
	}
	pg.BaseService = *service.NewBaseService(pg.logger, t.Name(), pg)
	require.NoError(t, pg.Start(ctx))
	<-entered
	pg.Stop()
	done := make(chan struct{})
	go func() { pg.Wait(); close(done) }()
	select {
	case <-done:
		t.Error("Wait returned before gossip handler finished")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("gossip handler did not drain")
	}
}
