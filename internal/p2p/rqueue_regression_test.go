package p2p

import (
	"context"
	"strconv"
	"testing"
	"time"

	gogotypes "github.com/cosmos/gogoproto/types"
	kitprometheus "github.com/go-kit/kit/metrics/prometheus"
	"github.com/prometheus/client_golang/prometheus"
	metricsdto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	tmsync "github.com/dashpay/tenderdash/internal/libs/sync"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

func TestSimpleQueueHeapLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := simpleQueueTestRouter(t, ctx, NopMetrics())
	for _, size := range []int{0, 1, 7, 8, queueBufferDefault, 4096} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			q := r.queueFactory(size).(*simpleQueue)
			defer q.close()
			require.Equal(t, simpleQueueCapacityMultiplier*size, q.maxSize)
		})
	}
	t.Run("retained", func(t *testing.T) {
		const size = 8
		q := r.queueFactory(size).(*simpleQueue)
		defer q.close()
		for i := 0; i < 256; i++ {
			select {
			case q.enqueue() <- Envelope{ChannelID: 1}:
			case <-time.After(time.Second):
				t.Fatal("queue stopped accepting envelopes")
			}
		}
		require.LessOrEqual(t, drainQueue(t, q), simpleQueueCapacityMultiplier*size+cap(q.input)+cap(q.output))
	})
}

func TestSimpleQueueChannelPriorities(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(strconv.FormatBool(late), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			r := simpleQueueTestRouter(t, ctx, NopMetrics())
			register := func() {
				for id, priority := range map[ChannelID]int{1: 20, 2: 2} {
					_, err := r.OpenChannel(ctx, &ChannelDescriptor{ID: id, Priority: priority, RecvBufferCapacity: 2})
					require.NoError(t, err)
				}
			}
			if !late {
				register()
			}
			q := r.getOrMakeQueue("peer", ChannelIDSet{1: {}, 2: {}}).(*simpleQueue)
			fillSimpleQueueOutput(q)
			if late {
				register()
			}
			q.enqueue() <- Envelope{ChannelID: 1}
			q.enqueue() <- Envelope{ChannelID: 2, Message: &gogotypes.StringValue{Value: "lower priority"}}
			waitSimpleQueueInput(t, q)
			drainSimpleQueueOutput(t, q)
			require.Equal(t, ChannelID(1), receiveSimpleQueue(t, q).ChannelID)
			require.Equal(t, ChannelID(2), receiveSimpleQueue(t, q).ChannelID)
		})
	}
}

func TestSimpleQueueTrimMetricAndDelivery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	drops := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_queue_drops"}, []string{"ch_id"})
	metrics := NopMetrics()
	metrics.PeerQueueDroppedMsgs = kitprometheus.NewCounter(drops)
	r := simpleQueueTestRouter(t, ctx, metrics)
	q := r.queueFactory(8).(*simpleQueue)
	fillSimpleQueueOutput(q)
	done := make([]<-chan struct{}, q.maxSize)
	for i := range done {
		e := Envelope{ChannelID: 1}
		done[i] = e.EnableDeliveryNotification()
		q.enqueue() <- e
	}
	q.enqueue() <- Envelope{ChannelID: 2, Message: &gogotypes.StringValue{Value: "retained"}}
	require.Eventually(t, func() bool {
		for _, delivered := range done {
			select {
			case <-delivered:
				return true
			default:
			}
		}
		return false
	}, time.Second, time.Millisecond, "trimming must notify the dropped envelope")
	for chID, expected := range map[string]float64{"1": 1, "2": 0} {
		var metric metricsdto.Metric
		require.NoError(t, drops.WithLabelValues(chID).Write(&metric))
		require.Equal(t, expected, metric.GetCounter().GetValue(), "channel %s", chID)
	}
}

func TestSimpleQueueConcurrentChannelRegistration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := simpleQueueTestRouter(t, ctx, NopMetrics())
	q := r.getOrMakeQueue("peer", nil).(*simpleQueue)
	fillSimpleQueueOutput(q)
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		for i := 0; i < 1000; i++ {
			select {
			case q.enqueue() <- Envelope{ChannelID: 1}:
			case <-ctx.Done():
				return
			}
		}
	}()
	for id := ChannelID(1); id <= 100; id++ {
		_, err := r.OpenChannel(ctx, &ChannelDescriptor{ID: id, Priority: int(id), RecvBufferCapacity: 2})
		require.NoError(t, err)
	}
	select {
	case <-producerDone:
	case <-ctx.Done():
		t.Fatal("enqueue stalled during channel registration")
	}
	q.enqueue() <- Envelope{ChannelID: 100}
	q.enqueue() <- Envelope{ChannelID: 1, Message: &gogotypes.StringValue{Value: "lower priority"}}
	waitSimpleQueueInput(t, q)
	drainSimpleQueueOutput(t, q)
	require.Equal(t, ChannelID(100), receiveSimpleQueue(t, q).ChannelID)
}

func simpleQueueTestRouter(t *testing.T, ctx context.Context, metrics *Metrics) *Router {
	t.Helper()
	r, err := NewRouter(log.NewNopLogger(), metrics, nil, nil,
		func() *types.NodeInfo {
			return &types.NodeInfo{Channels: tmsync.NewConcurrentSlice[uint16]()}
		}, &MemoryTransport{}, nil,
		RouterOptions{QueueType: queueTypeSimplePriority})
	require.NoError(t, err)
	require.NoError(t, r.setupQueueFactory(ctx))
	return r
}

func fillSimpleQueueOutput(q *simpleQueue) {
	for i := 0; i < cap(q.output); i++ {
		q.output <- Envelope{}
	}
}

func waitSimpleQueueInput(t *testing.T, q *simpleQueue) {
	t.Helper()
	// Receiving the marker means all earlier envelopes have entered the heap.
	q.enqueue() <- Envelope{}
	require.Eventually(t, func() bool { return len(q.input) == 0 }, time.Second, time.Millisecond)
}

func drainSimpleQueueOutput(t *testing.T, q *simpleQueue) {
	t.Helper()
	for i := 0; i < cap(q.output); i++ {
		require.Zero(t, receiveSimpleQueue(t, q).ChannelID)
	}
}

func receiveSimpleQueue(t *testing.T, q *simpleQueue) Envelope {
	t.Helper()
	select {
	case e := <-q.dequeue():
		return e
	case <-time.After(time.Second):
		t.Fatal("queue did not deliver an envelope")
		return Envelope{}
	}
}
