package p2p

import (
	"context"
	"testing"
	"time"

	kitprometheus "github.com/go-kit/kit/metrics/prometheus"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	p2pproto "github.com/dashpay/tenderdash/proto/tendermint/p2p"
	"github.com/dashpay/tenderdash/types"
)

func TestRouterFullPeerQueueDoesNotBlockOtherPeers(t *testing.T) {
	for _, broadcast := range []bool{false, true} {
		name := "unicast"
		if broadcast {
			name = "broadcast"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			const slow, healthy types.NodeID = "slow", "healthy"
			const chID = ConsensusStateChannel
			fullQueue, healthyQueue := newFIFOQueue(1), newFIFOQueue(2)
			fullQueue.enqueue() <- Envelope{Message: &tmcons.NewRoundStep{Height: 1}}
			metrics := NopMetrics()
			drops := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "drops"}, []string{"ch_id"})
			metrics.PeerQueueDroppedMsgs = kitprometheus.NewCounter(drops)
			router := &Router{
				logger:       log.NewNopLogger(),
				metrics:      metrics,
				peerQueues:   map[types.NodeID]queue{slow: fullQueue, healthy: healthyQueue},
				peerChannels: map[types.NodeID]ChannelIDSet{slow: {chID: {}}, healthy: {chID: {}}},
			}
			out := make(chan Envelope, 2)
			blocked := Envelope{To: slow, Broadcast: broadcast, Message: &tmcons.NewRoundStep{Height: 2}}
			var delivered <-chan struct{}
			if !broadcast {
				delivered = blocked.EnableDeliveryNotification()
			}
			out <- blocked
			out <- Envelope{To: healthy, Message: &tmcons.NewRoundStep{Height: 3}}
			stopped := make(chan struct{})
			go func() {
				defer close(stopped)
				router.routeChannel(ctx, chID, out, nil)
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case <-stopped:
				case <-time.After(time.Second):
					t.Error("router did not stop")
				}
			})

			heights := []int64{3}
			if broadcast {
				heights = []int64{2, 3}
			}
			for _, height := range heights {
				select {
				case envelope := <-healthyQueue.dequeue():
					require.Equal(t, chID, envelope.ChannelID)
					require.Equal(t, height, envelope.Message.(*tmcons.NewRoundStep).Height)
				case <-ctx.Done():
					t.Fatal("a full peer queue blocked delivery to a healthy peer")
				}
			}
			if !broadcast {
				select {
				case <-delivered:
				case <-ctx.Done():
					t.Fatal("dropped message did not release its delivery waiter")
				}
			}
			// A later announcement can reach the peer once it drains its queue.
			<-fullQueue.dequeue()
			out <- Envelope{To: slow, Message: &tmcons.NewRoundStep{Height: 4}}
			select {
			case envelope := <-fullQueue.dequeue():
				require.Equal(t, int64(4), envelope.Message.(*tmcons.NewRoundStep).Height)
			case <-ctx.Done():
				t.Fatal("the peer did not recover after queue congestion")
			}
			cancel()
			<-stopped
			require.Empty(t, router.deliveries)
			var metric dto.Metric
			require.NoError(t, drops.WithLabelValues("32").Write(&metric))
			require.Equal(t, float64(1), metric.Counter.GetValue())
		})
	}
}

func TestRouterOtherChannelsKeepPeerQueueBackpressure(t *testing.T) {
	for name, pending := range map[string]Envelope{
		"data": {ChannelID: ConsensusDataChannel, Message: &tmcons.Proposal{}},
		"pex":  {ChannelID: 0x00, Message: &p2pproto.PexRequest{}},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			const peer types.NodeID = "peer"
			chID := pending.ChannelID
			peerQueue := newFIFOQueue(1)
			peerQueue.enqueue() <- Envelope{To: peer}
			router := &Router{
				logger:       log.NewNopLogger(),
				metrics:      NopMetrics(),
				peerQueues:   map[types.NodeID]queue{peer: peerQueue},
				peerChannels: map[types.NodeID]ChannelIDSet{peer: {chID: {}}},
			}
			out := make(chan Envelope)
			stopped := make(chan struct{})
			go func() {
				defer close(stopped)
				router.routeChannel(ctx, chID, out, nil)
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case <-stopped:
				case <-time.After(time.Second):
					t.Error("router did not stop")
				}
			})
			pending.To = peer
			delivered := pending.EnableDeliveryNotification()
			select {
			case out <- pending:
			case <-ctx.Done():
				t.Fatal("router did not accept the message")
			}
			select {
			case <-delivered:
				t.Fatal("a non-state message was dropped on a full peer queue")
			case <-time.After(20 * time.Millisecond):
			}
			<-peerQueue.dequeue()
			select {
			case envelope := <-peerQueue.dequeue():
				require.Same(t, pending.Message, envelope.Message)
				envelope.NotifyDelivery()
			case <-ctx.Done():
				t.Fatal("the pending message was not delivered after the queue drained")
			}
		})
	}
}
