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

// A handler whose sleep is zero runs back to back, and the round-state handler
// sends on every pass, so a config of 0s — or a stray "2" read as 2ns — would
// flood every peer. The worker runs no handler faster than the floor, whatever
// the config says and whether or not it was validated.
func TestPeerGossipWorkerFloorsSleepDurations(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cs, _ := makeState(ctx, t, makeStateArgs{})
	ps := NewPeerState(log.NewNopLogger(), types.NodeID("peer"))
	sleepsFor := func(gossip, queryMaj23 time.Duration) []time.Duration {
		cs.config.PeerGossipSleepDuration = gossip
		cs.config.PeerQueryMaj23SleepDuration = queryMaj23
		pgw := newPeerGossipWorker(log.NewNopLogger(), ps, cs, &p2pMsgSender{logger: log.NewNopLogger(), ps: ps})
		sleeps := make([]time.Duration, 0, len(pgw.handlers))
		for _, hd := range pgw.handlers {
			sleeps = append(sleeps, hd.sleepDuration)
		}
		return sleeps
	}

	for _, sleep := range []time.Duration{0, -time.Second, minGossipSleepDuration / 2} {
		for i, got := range sleepsFor(sleep, sleep) {
			require.Equal(t, minGossipSleepDuration, got, "handler %d configured at %s", i, sleep)
		}
	}

	kept := sleepsFor(minGossipSleepDuration, 3*minGossipSleepDuration)
	require.Contains(t, kept, minGossipSleepDuration)
	require.Contains(t, kept, 3*minGossipSleepDuration)
}
