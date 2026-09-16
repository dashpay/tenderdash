package p2p

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	p2pproto "github.com/dashpay/tenderdash/proto/tendermint/p2p"
	"github.com/dashpay/tenderdash/types"
)

func TestCompletePeerDeliveriesReleasesPendingWaiters(t *testing.T) {
	peerID := types.NodeID("0102030405060708090a0b0c0d0e0f1011121314")
	router := &Router{}
	envelopes := []*Envelope{{}, {}}
	done := make([]<-chan struct{}, len(envelopes))
	for i, envelope := range envelopes {
		done[i] = envelope.EnableDeliveryNotification()
		router.trackDelivery(peerID, envelope)
	}

	router.completePeerDeliveries(peerID)
	for _, delivered := range done {
		select {
		case <-delivered:
		case <-time.After(time.Second):
			t.Fatal("peer shutdown did not release a pending delivery")
		}
	}
	require.Empty(t, router.deliveries)
}

func TestDeliveryProgressIsCoalesced(t *testing.T) {
	envelope := &Envelope{}
	envelope.EnableDeliveryNotification()
	envelope.NotifyDeliveryProgress()
	envelope.NotifyDeliveryProgress()

	select {
	case <-envelope.DeliveryProgress():
	default:
		t.Fatal("delivery progress was not reported")
	}
	select {
	case <-envelope.DeliveryProgress():
		t.Fatal("duplicate progress notification was not coalesced")
	default:
	}
}

func TestSimplePriorityQueueDoesNotPopWhenOutputIsFull(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	queue := newSimplePriorityQueue(ctx, 2)
	for i := 0; i < 3; i++ {
		queue.enqueue() <- Envelope{
			To:      types.NodeID(string(rune('a' + i))),
			Message: &p2pproto.Envelope{},
		}
	}
	require.Eventually(t, func() bool {
		return len(queue.input) == 0 && len(queue.output) == 1
	}, time.Second, time.Millisecond)
	time.Sleep(20 * time.Millisecond)

	seen := make(map[types.NodeID]struct{})
	for i := 0; i < 3; i++ {
		select {
		case envelope := <-queue.dequeue():
			seen[envelope.To] = struct{}{}
		case <-time.After(time.Second):
			t.Fatal("queue dropped an envelope while its output was full")
		}
	}
	require.Len(t, seen, 3)
}
