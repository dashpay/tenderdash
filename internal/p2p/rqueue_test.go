package p2p

import (
	"container/heap"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSimpleQueue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// set up a small queue with very small buffers so we can
	// watch it shed load, then send a bunch of messages to the
	// queue, most of which we'll watch it drop.
	sq := newSimplePriorityQueue(ctx, 1)
	for i := 0; i < 100; i++ {
		sq.enqueue() <- Envelope{From: "merlin"}
	}

	seen := 0

RETRY:
	for seen <= 2 {
		select {
		case e := <-sq.dequeue():
			if e.From != "merlin" {
				continue
			}
			seen++
		case <-time.After(10 * time.Millisecond):
			break RETRY
		}
	}
	// if we don't see any messages, then it's just broken.
	if seen == 0 {
		t.Errorf("seen %d messages, should have seen more than one", seen)
	}
	// ensure that load shedding happens: there can be at most 3
	// messages that we get out of this, one that was buffered
	// plus 2 that were under the cap, everything else gets
	// dropped.
	if seen > 3 {
		t.Errorf("saw %d messages, should have seen 5 or fewer", seen)
	}

}

// An overflowing heap is trimmed far enough below maxSize that the next pushes
// do not each need another sort. It keeps the highest-ranked envelopes, stays a
// valid heap, and releases both the senders waiting on what it drops and the
// memory those envelopes hold.
func TestSimpleQueueTrimDropsLowestRankedBelowMaxSize(t *testing.T) {
	testCases := []struct {
		name    string
		maxSize int
		keep    int
	}{
		{name: "large enough for a margin", maxSize: 64, keep: 60},
		{name: "too small for a margin", maxSize: 4, keep: 4},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			q := &simpleQueue{maxSize: tc.maxSize}
			overflow := tc.maxSize + 1

			// Timestamps this far apart decide the rank on their own, and the
			// newest ranks highest, so the oldest are the ones to lose.
			start := time.Now()
			pq := make(priorityQueue, 0, overflow)
			delivered := make([]<-chan struct{}, overflow)
			timestamps := make([]time.Time, overflow)
			for i := range overflow {
				pqEnv := &pqEnvelope{timestamp: start.Add(time.Duration(i) * time.Second)}
				delivered[i] = pqEnv.envelope.EnableDeliveryNotification()
				timestamps[i] = pqEnv.timestamp
				heap.Push(&pq, pqEnv)
			}

			trimmed := q.trim(pq)
			require.Len(t, trimmed, tc.keep)

			firstKept := overflow - tc.keep
			for i, done := range delivered {
				select {
				case <-done:
					require.Less(t, i, firstKept, "released the sender of a kept envelope")
				default:
					require.GreaterOrEqual(t, i, firstKept, "did not release the sender of a dropped envelope")
				}
			}
			for _, slot := range trimmed[tc.keep:overflow] {
				require.Nil(t, slot, "a dropped envelope is still reachable")
			}

			for i := overflow - 1; i >= firstKept; i-- {
				next, ok := heap.Pop(&trimmed).(*pqEnvelope)
				require.True(t, ok)
				require.True(t, timestamps[i].Equal(next.timestamp), "the trimmed heap is out of order")
			}
			require.Empty(t, trimmed)
		})
	}
}
