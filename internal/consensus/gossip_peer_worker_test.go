package consensus

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/service"
	"github.com/dashpay/tenderdash/types"
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

// A handler whose sleep is zero runs back to back, spinning a goroutine per
// handler per peer, and a config of 0s — or a stray "2" read as 2ns — is not
// caught where validation is skipped. The reactor floors the cadence once, for
// every consumer of the two settings.
func TestFlooredGossipCadence(t *testing.T) {
	for name, tc := range map[string]struct {
		gossip, queryMaj23 time.Duration
		want               gossipCadence
	}{
		"zero":            {0, 0, gossipCadence{minGossipSleepDuration, minGossipSleepDuration}},
		"negative":        {-time.Second, -time.Second, gossipCadence{minGossipSleepDuration, minGossipSleepDuration}},
		"below the floor": {minGossipSleepDuration / 2, minGossipSleepDuration / 2, gossipCadence{minGossipSleepDuration, minGossipSleepDuration}},
		"at the floor":    {minGossipSleepDuration, minGossipSleepDuration, gossipCadence{minGossipSleepDuration, minGossipSleepDuration}},
		"each on its own": {0, 3 * minGossipSleepDuration, gossipCadence{minGossipSleepDuration, 3 * minGossipSleepDuration}},
		"defaults kept":   {100 * time.Millisecond, 2 * time.Second, gossipCadence{100 * time.Millisecond, 2 * time.Second}},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := config.DefaultConsensusConfig()
			cfg.PeerGossipSleepDuration, cfg.PeerQueryMaj23SleepDuration = tc.gossip, tc.queryMaj23
			require.Equal(t, tc.want, flooredGossipCadence(log.NewNopLogger(), cfg))
		})
	}
}

func TestReactorFloorsGossipCadence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cs, _ := makeState(ctx, t, makeStateArgs{})
	cs.config.PeerGossipSleepDuration = 0
	cs.config.PeerQueryMaj23SleepDuration = 0
	r := NewReactor(log.NewNopLogger(), cs, nil, nil, cs.eventBus, false, NopMetrics())
	require.Equal(t, gossipCadence{minGossipSleepDuration, minGossipSleepDuration}, r.cadence)

	ps := NewPeerState(log.NewNopLogger(), types.NodeID("peer"))
	pgw := newPeerGossipWorker(log.NewNopLogger(), ps, cs, &p2pMsgSender{logger: log.NewNopLogger(), ps: ps}, r.cadence)
	for i, hd := range pgw.handlers {
		require.Equal(t, minGossipSleepDuration, hd.sleepDuration, "handler %d", i)
	}
}

// A peer gossip worker starts and stops with every peer connection, so its
// lifecycle must not reach Info-level logs.
func TestPeerGossipWorkerLogsLifecycleAtTrace(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cs, _ := makeState(ctx, t, makeStateArgs{})
	for level, want := range map[string]bool{log.LogLevelInfo: false, log.LogLevelTrace: true} {
		logs := &bytes.Buffer{}
		logger, err := log.NewLogger(level, logs)
		require.NoError(t, err)
		ps := NewPeerState(log.NewNopLogger(), types.NodeID("peer"))
		pgw := newPeerGossipWorker(logger, ps, cs, &p2pMsgSender{logger: logger, ps: ps}, gossipCadence{time.Hour, time.Hour})
		pgw.handlers = nil
		require.NoError(t, pgw.Start(ctx))
		pgw.Stop()
		pgw.Wait()
		require.Equal(t, want, strings.Contains(logs.String(), "stopped service"), "level %s: %s", level, logs)
	}
}
