package blocksync

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/eventbus"
	clientmocks "github.com/dashpay/tenderdash/internal/p2p/client/mocks"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/service"
	"github.com/dashpay/tenderdash/libs/workerpool"
)

func TestSwitchToBlockSyncStartsAfterSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		initialHeight, peerBase int64
		retry                   bool
	}{
		{name: "full history", initialHeight: 1, peerBase: 1},
		{name: "pruned history", initialHeight: 1, peerBase: 100},
		{name: "custom initial height and stale retry", initialHeight: 10, peerBase: 100, retry: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer leaktest.Check(t)()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			requests := make(chan int64, 1)
			blockClient := clientmocks.NewBlockClient(t)
			blockClient.On("GetBlock", mock.Anything, mock.Anything, mock.Anything).
				Maybe().Run(func(args mock.Arguments) {
				requests <- args.Get(1).(int64)
				<-args.Get(0).(context.Context).Done()
			}).Return(nil, context.Canceled)
			applier := newBlockApplier(nil, nil)
			synchronizer := NewSynchronizer(tc.initialHeight, blockClient, applier,
				WithWorkerPool(workerpool.New(1)))
			synchronizer.AddPeer(newPeerData("peer", tc.peerBase, 101))
			if tc.retry {
				synchronizer.jobGen.pushBack(tc.initialHeight)
			}
			bus := eventbus.NewDefault(log.NewNopLogger())
			require.NoError(t, bus.Start(ctx))
			defer bus.Wait()
			defer cancel()
			reactor := &Reactor{
				executor: applier, synchronizer: synchronizer, blockSyncFlag: new(atomic.Bool),
				eventBus: bus, statusUpdateInterval: time.Hour,
			}
			state := sm.State{InitialHeight: tc.initialHeight, LastBlockHeight: 100}
			require.NoError(t, reactor.SwitchToBlockSync(ctx, state))
			defer synchronizer.Wait()
			defer cancel()
			select {
			case height := <-requests:
				require.Equal(t, int64(101), height, "first request must follow the state-sync snapshot")
			case <-ctx.Done():
				t.Fatal("no block requested from a peer serving the next height")
			}
			synchronizer.mtx.RLock()
			startHeight := synchronizer.startHeight
			synchronizer.mtx.RUnlock()
			require.Equal(t, int64(101), startHeight, "sync metrics must start after the snapshot")
		})
	}
}

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
