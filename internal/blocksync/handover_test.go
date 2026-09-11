package blocksync

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/service"
)

func TestWaitForSyncRetainsObservedTarget(t *testing.T) {
	clock := clockwork.NewFakeClock()
	observed := make(chan struct{}, 1)
	synchronizer := NewSynchronizer(100, nil, nil, WithClock(clock),
		WithLogger(&handoverObservationLogger{Logger: log.NewNopLogger(), observed: observed}))
	synchronizer.lastAdvance = clock.Now()
	synchronizer.AddPeer(newPeerData("peer", 1, 101))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan int64, 1)
	go func() {
		_, target := synchronizer.WaitForSync(ctx)
		result <- target
	}()
	require.NoError(t, clock.BlockUntilContext(ctx, 1))
	clock.Advance(switchToConsensusIntervalSeconds * time.Second)
	select {
	case <-observed:
	case <-ctx.Done():
		t.Fatal("synchronizer did not observe its peer")
	}
	synchronizer.RemovePeer("peer")
	clock.Advance(syncTimeout + time.Second)
	select {
	case target := <-result:
		require.Equal(t, int64(101), target, "disconnecting a peer must not erase its observed handover target")
	case <-ctx.Done():
		t.Fatal("synchronizer did not finish")
	}
}

type handoverObservationLogger struct {
	log.Logger
	observed chan<- struct{}
}

func (l *handoverObservationLogger) Info(string, ...interface{}) {
	select {
	case l.observed <- struct{}{}:
	default:
	}
}

func TestHandoverUsesFinalAppliedState(t *testing.T) {
	for _, applyDuringStop := range []bool{false, true} {
		t.Run(map[bool]string{false: "one block remains", true: "last block applied during stop"}[applyDuringStop], func(t *testing.T) {
			applier := newBlockApplier(nil, nil, applierWithState(sm.State{LastBlockHeight: 99}))
			synchronizer := NewSynchronizer(100, nil, applier)
			synchronizer.AddPeer(newPeerData("peer", 1, 100))
			stop := &handoverStopHook{fn: func() {
				if applyDuringStop {
					applier.UpdateState(sm.State{LastBlockHeight: 100})
				}
			}}
			synchronizer.BaseService = *service.NewBaseService(log.NewNopLogger(), "handover", stop)
			serviceCtx, cancelService := context.WithCancel(context.Background())
			defer cancelService()
			require.NoError(t, synchronizer.Start(serviceCtx))
			capture := &handoverCapture{}
			reactor := &Reactor{executor: applier, synchronizer: synchronizer, consReactor: capture, blockSyncFlag: new(atomic.Bool)}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			reactor.poolRoutine(ctx, false)
			require.True(t, capture.called)
			require.True(t, capture.skipWAL)
			require.Equal(t, applier.State().LastBlockHeight, capture.state.LastBlockHeight)
			require.Equal(t, int64(100), capture.targetHeight)
		})
	}
}

type handoverCapture struct {
	state           sm.State
	called, skipWAL bool
	targetHeight    int64
}

func (c *handoverCapture) SwitchToConsensus(_ context.Context, state sm.State, skipWAL bool, targetHeight int64) {
	c.called, c.state, c.skipWAL, c.targetHeight = true, state, skipWAL, targetHeight
}

type handoverStopHook struct{ fn func() }

func (*handoverStopHook) OnStart(context.Context) error { return nil }
func (h *handoverStopHook) OnStop()                     { h.fn() }
