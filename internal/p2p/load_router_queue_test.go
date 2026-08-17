package p2p

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
)

// The consensus reactor's own bounds start where the router hands a message
// over. Everything a peer sends before that sits in the router's shared inbound
// queue for the channel — one queue for every peer — so what that queue will
// hold, and what it does when it will hold no more, is part of what a flood
// costs this node.
//
// This records both. The numbers are the ones an operator would need and
// cannot currently read off a metric: the shared inbound queue has neither an
// occupancy gauge nor a drop counter, only the latency histogram of enqueueing
// into it.
func TestLoadRouterSharedQueueOccupancyAndDrops(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The production capacity squared is far too large to fill in a test, and
	// the shape is what is being pinned, not the constant. This is the same
	// queue the router builds, at a size small enough to reach its ceiling.
	const capacity = 8
	q := newSimplePriorityQueue(ctx, capacity)
	ceiling := capacity * capacity

	// Nothing drains while this runs, which is the case that matters: a
	// consumer keeping up leaves the queue empty whatever arrives.
	const offered = 40 * capacity * capacity
	for i := 0; i < offered; i++ {
		select {
		case q.enqueue() <- Envelope{ChannelID: ConsensusVoteChannel, Message: &tmcons.Vote{}}:
		case <-time.After(5 * time.Second):
			t.Fatal("the queue stopped accepting, so the ceiling cannot be measured")
		}
	}

	held := drainQueue(t, q)
	reportf(t, "shared inbound queue at capacity %d: %d envelopes offered, %d retained "+
		"(ceiling %d), %d discarded without a metric or a peer error",
		capacity, offered, held, ceiling, offered-held)

	require.LessOrEqual(t, held, ceiling+capacity,
		"the shared inbound queue retained more than its ceiling, so its memory is unbounded")
	require.Positive(t, held, "the queue retained nothing, so it was never exercised")

	// What the same arithmetic gives at the shipped capacities. These are the
	// figures the consensus-side ceilings sit behind: the peer lanes bound what
	// this node has admitted, and this bounds what is waiting to be.
	for _, chDesc := range ConsensusChannelDescriptors() {
		size := chDesc.RecvBufferCapacity
		reportf(t, "channel %q: shared inbound queue holds up to %d envelopes of up to %d bytes",
			chDesc.Name, size*size, chDesc.RecvMessageCapacity)
	}
}

// An inbound consensus queue that fills must make the sender wait rather than
// quietly throw messages away: a drop there is invisible to the consensus
// reactor's own accounting and to the operator alike, since nothing counts it.
//
// The mempool channel opts into dropping by setting an enqueue timeout. The
// consensus channels must not, and this fails if one starts to — not because
// dropping is wrong in itself, but because there is no metric to see it by.
func TestLoadConsensusChannelsDoNotSilentlyTimeOutOnEnqueue(t *testing.T) {
	for _, chDesc := range ConsensusChannelDescriptors() {
		require.Zero(t, chDesc.EnqueueTimeout,
			"channel %q would drop inbound messages on a full queue, and nothing counts those drops",
			chDesc.Name)
	}
}

// drainQueue takes everything the queue will give up without waiting for more.
func drainQueue(t *testing.T, q queue) int {
	t.Helper()
	held := 0
	for {
		select {
		case <-q.dequeue():
			held++
		case <-time.After(500 * time.Millisecond):
			return held
		}
	}
}

func reportf(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Logf("MEASURED "+format, args...)
}
