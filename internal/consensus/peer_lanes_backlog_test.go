package consensus

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto/merkle"
	"github.com/dashpay/tenderdash/types"
)

func blockPartLaneMsg(peerID types.NodeID) msgInfo {
	return msgInfo{
		Msg: &BlockPartMessage{
			Height: 1,
			Round:  0,
			Part: &types.Part{
				Index: 0,
				Bytes: make([]byte, types.BlockPartSizeBytes),
				Proof: merkle.Proof{Total: 1},
			},
		},
		PeerID: peerID,
	}
}

// Counting messages does not bound memory: the lanes hold whatever arrives, and
// a block part is two orders of magnitude larger than a vote, so a backlog
// bounded only in messages is a backlog of block parts bounded only by the
// message count times the largest message.
func TestPeerLanesBoundQueuedBytes(t *testing.T) {
	ctx := context.Background()
	lanes := newPeerLanes()

	// More full-size block parts than the byte ceiling can hold, spread over
	// enough lanes that neither a single lane's capacity nor the message count
	// is what stops them.
	const peers = 64
	for i := 0; i < peers; i++ {
		peer := types.NodeID(string(rune('a' + i%26)))
		for j := 0; j < laneCapacity; j++ {
			require.NoError(t, lanes.send(ctx, blockPartLaneMsg(peer)))
		}
	}

	assert.LessOrEqual(t, lanes.bufferedBytes(), laneByteCapacity,
		"the lanes must not hold more payload than the node is willing to pin")
	assert.Less(t, lanes.buffered(), lanes.bufferCapacity,
		"the byte ceiling must be what binds for block parts, not the message count")
}

// A vote is small, so the byte ceiling must not be what limits a backlog of
// them: the message count is still the bound that matters there.
func TestPeerLaneByteCeilingDoesNotBindOnVotes(t *testing.T) {
	ctx := context.Background()
	lanes := newPeerLanes()

	for i := 0; i < 26; i++ {
		peer := types.NodeID(string(rune('a' + i)))
		for j := 0; j < laneCapacity; j++ {
			require.NoError(t, lanes.send(ctx, cheapLaneMsg(peer)))
		}
	}

	assert.Equal(t, 26*laneCapacity, lanes.buffered(),
		"a backlog of votes must be bounded by the message count, not by bytes")
}

// The byte ceiling must take its slot from whoever is pinning the most memory.
// Taking it from a lane holding a handful of votes would let one peer's block
// parts silence everyone else.
func TestPeerLaneByteCeilingShedsFromTheHeaviestLane(t *testing.T) {
	ctx := context.Background()

	partSize := int(types.BlockPartSizeBytes)
	const hogParts = 8
	lanes := newPeerLanes(withLaneByteCapacity(hogParts * partSize))

	for i := 0; i < hogParts; i++ {
		require.NoError(t, lanes.send(ctx, blockPartLaneMsg("hog")))
	}
	// The light lane holds many messages but almost no memory.
	for i := 0; i < 32; i++ {
		require.NoError(t, lanes.send(ctx, cheapLaneMsg("light")))
	}

	assert.Equal(t, 32, len(lanes.lanes["light"].queue),
		"the peer pinning the least memory must not pay for the one pinning the most")
	assert.Less(t, len(lanes.lanes["hog"].queue), hogParts,
		"room must be made where the memory actually is")
	assert.LessOrEqual(t, lanes.bufferedBytes(), hogParts*partSize)
}

// The internal queue carries this node's OWN messages -- the votes and
// proposals it has just signed -- and consensus cannot progress without them.
// They are read by a goroutine of their own and handed to the consensus
// goroutine independently of the peer scheduler, which is what reserves
// internal progress: a peer backlog, however large, cannot get in front of
// them.
func TestInternalMessagesAreServedWhileThePeerLanesAreSaturated(t *testing.T) {
	// The number of peer messages served before this node's own must not grow
	// with the backlog. Measuring it at two very different backlog sizes is
	// what distinguishes a reservation from a queue that merely happens to be
	// short: with one shared queue the answer would be the backlog itself.
	small := internalMessageWait(t, 200)
	large := internalMessageWait(t, 5000)

	assert.Less(t, large, 8,
		"this node's own message must not queue behind the peer backlog")
	assert.Less(t, large, small+8,
		"the wait for this node's own message must not scale with what peers have queued")
}

// internalMessageWait reports how many peer messages are handed to the
// consensus goroutine after one of this node's own is submitted and before it
// is served, with backlog peer messages already waiting.
func internalMessageWait(t *testing.T, backlog int) int {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	queue := newMsgInfoQueue()
	go queue.fanIn(ctx)
	defer queue.stop()

	for i := 0; i < backlog; i++ {
		require.NoError(t, queue.send(ctx, cheapLaneMsg("flood").Msg, "flood"))
	}

	// Every peer message is settled as the consensus goroutine would, so the
	// scheduler keeps handing them over and the two readers really do compete.
	// The measurement starts once the queue is running at full stretch.
	const submitAfter = 10
	served, submitted := 0, false
	for mi := range queue.read() {
		if mi.PeerID == "" {
			return served
		}
		served++
		queue.settlePeerMsg()
		if !submitted && served == submitAfter {
			require.NoError(t, queue.send(ctx, cheapLaneMsg("").Msg, ""))
			submitted = true
			served = 0
		}
	}
	t.Fatal("this node's own message was never served")
	return 0
}
