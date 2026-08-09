package consensus

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cosmos/gogoproto/proto"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto/merkle"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// cheapLaneMsg is the cheapest message a peer can put on a lane: a prevote,
// priced at one signature verification.
func cheapLaneMsg(peerID types.NodeID) msgInfo {
	return msgInfo{
		Msg:    &VoteMessage{Vote: &types.Vote{Type: tmproto.PrevoteType, Height: 1}},
		PeerID: peerID,
	}
}

// expensiveLaneMsg is a precommit carrying n vote extensions, priced at one
// verification of the block signature and one per extension.
func expensiveLaneMsg(peerID types.NodeID, extensions int) msgInfo {
	return msgInfo{Msg: &VoteMessage{Vote: testPrecommitVote(extensions)}, PeerID: peerID}
}

// laneMsg builds a message of the given extension count, or the cheapest message
// there is when that count is negative.
func laneMsg(peerID types.NodeID, extensions int) msgInfo {
	if extensions < 0 {
		return cheapLaneMsg(peerID)
	}
	return expensiveLaneMsg(peerID, extensions)
}

// drainLane takes everything the rotation currently owes a turn to.
func drainLanes(t *testing.T, lanes *peerLanes) []msgInfo {
	t.Helper()
	var served []msgInfo
	for {
		mi, ok := lanes.next()
		if !ok {
			return served
		}
		served = append(served, mi)
	}
}

// A peer that becomes active joins the rotation behind the peers already
// waiting. Inserting anywhere else would let a peer that keeps its lane
// oscillating between empty and full buy itself extra turns.
func TestPeerLanesNewLaneJoinsRotationAtTail(t *testing.T) {
	ctx := context.Background()
	lanes := newPeerLanes()

	require.NoError(t, lanes.send(ctx, cheapLaneMsg("first")))
	require.NoError(t, lanes.send(ctx, cheapLaneMsg("second")))
	require.NoError(t, lanes.send(ctx, cheapLaneMsg("third")))

	var order []types.NodeID
	for _, mi := range drainLanes(t, lanes) {
		order = append(order, mi.PeerID)
	}
	require.Equal(t, []types.NodeID{"first", "second", "third"}, order)

	// A lane that ran dry and comes back is new again: it joins at the tail
	// rather than resuming where it left off.
	require.NoError(t, lanes.send(ctx, cheapLaneMsg("second")))
	require.NoError(t, lanes.send(ctx, cheapLaneMsg("first")))

	order = order[:0]
	for _, mi := range drainLanes(t, lanes) {
		order = append(order, mi.PeerID)
	}
	require.Equal(t, []types.NodeID{"second", "first"}, order)
}

// The rotation is fair in verification work, not in message count: what other
// lanes may complete ahead of a peer's head is bounded by that head's own cost,
// however cheap or expensive their messages are.
//
// Both directions matter and each pins something different. With an expensive
// head against a cheap flood the bound is what stops the flood from overtaking
// it for as long as its sender keeps sending — a rotation blind to cost serves
// that head sooner, so this direction bounds the wait rather than telling the
// two apart. With a cheap head against an expensive flood the two part company:
// a rotation blind to cost hands every other lane the most expensive message
// the protocol allows, per turn, at the waiting peer's expense.
func TestPeerLanesBoundWorkAheadOfAHead(t *testing.T) {
	const attackers = 8

	testCases := []struct {
		name string
		// extensions on the honest and attacker heads; -1 is the cheapest
		// message there is.
		honest   int
		attacker int
	}{
		{name: "expensive head, cheap flood", honest: 4, attacker: -1},
		{name: "cheap head, expensive flood", honest: -1, attacker: types.MaxVoteExtensions},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			lanes := newPeerLanes()

			honest := laneMsg("honest", tc.honest)
			honestCost := laneTurnCost(honest)

			// Attacker lanes hold far more than the honest head can possibly wait
			// for, so nothing but the rotation limits what they get through.
			for i := 0; i < attackers; i++ {
				peerID := types.NodeID(fmt.Sprintf("attacker-%d", i))
				for j := 0; j < 4*honestCost+4; j++ {
					require.NoError(t, lanes.send(ctx, laneMsg(peerID, tc.attacker)))
				}
			}
			require.NoError(t, lanes.send(ctx, honest))

			work := 0
			for {
				mi, ok := lanes.next()
				require.True(t, ok, "the honest head was never served")
				if mi.PeerID == "honest" {
					break
				}
				work += laneTurnCost(mi)
			}

			require.LessOrEqual(t, work, attackers*max(laneQuantum, honestCost),
				"other lanes completed more work ahead of the honest head than its own cost allows")
		})
	}
}

// The rotation's service order is logical, not physical: a lane that runs dry
// and rejoins goes behind everyone already waiting and cannot deny a waiting
// lane its turn. Two lanes that repeatedly drain and reactivate around an
// expensive head must not keep the rotation cycling among themselves while that
// head is never revisited.
//
// This is the oscillation that a physical cursor into the rotation slice
// permits: with the honest head at the front and two cost-1 lanes draining and
// reactivating behind it, the cursor chases the churn at a fixed nonzero
// position and the honest head accrues its first quantum but never another, so
// it is starved indefinitely. A stable logical rotation returns to it within a
// bounded number of the other lanes' turns.
func TestPeerLanesReactivationCannotStarveAnExpensiveHead(t *testing.T) {
	ctx := context.Background()
	lanes := newPeerLanes()

	honest := expensiveLaneMsg("honest", 4)
	honestCost := laneTurnCost(honest)
	require.Greater(t, honestCost, laneQuantum,
		"the head must cost more than one turn grants, or it cannot be starved by churn")

	require.NoError(t, lanes.send(ctx, honest))
	require.NoError(t, lanes.send(ctx, cheapLaneMsg("b")))
	require.NoError(t, lanes.send(ctx, cheapLaneMsg("c")))

	// Each turn serves one of the two cheap lanes, which immediately reactivates.
	// The honest head must still be served within a bounded number of turns:
	// each rotation of the two attacker lanes grants it one quantum, so it is due
	// after about its own cost's worth of rotations. The cap is far above that
	// bound and far below "never".
	const maxTurns = 1000
	served := false
	for i := 0; i < maxTurns; i++ {
		mi, ok := lanes.next()
		require.True(t, ok)
		if mi.PeerID == "honest" {
			served = true
			break
		}
		require.NoError(t, lanes.send(ctx, cheapLaneMsg(mi.PeerID)))
	}
	require.True(t, served,
		"an expensive honest head was starved by two lanes draining and reactivating around it")
}

// bigPrecommitVote builds a precommit carrying the maximum number of vote
// extensions, each with a payload of the given size, so its retained memory is
// dominated by attacker-chosen bytes rather than by fixed fields.
func bigPrecommitVote(extensions, extBytes int) *types.Vote {
	exts := make([]*tmproto.VoteExtension, extensions)
	for i := range exts {
		exts[i] = &tmproto.VoteExtension{
			Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
			Extension: make([]byte, extBytes),
		}
	}
	ve, err := types.VoteExtensionsFromProto(exts...)
	if err != nil {
		panic(err)
	}
	return &types.Vote{
		Type:           tmproto.PrecommitType,
		Height:         1,
		VoteExtensions: ve,
	}
}

// bigCommit builds a commit carrying the maximum number of threshold vote
// extensions, each with a payload of the given size.
func bigCommit(extensions, extBytes int) *types.Commit {
	exts := make([]*tmproto.VoteExtension, extensions)
	for i := range exts {
		exts[i] = &tmproto.VoteExtension{
			Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
			Extension: make([]byte, extBytes),
		}
	}
	return &types.Commit{Height: 1, ThresholdVoteExtensions: exts}
}

// The shared byte ceiling must bound the memory really pinned, not the memory a
// flat per-message overhead pretends is pinned. A vote or commit can carry up
// to the maximum number of vote extensions, whose payload length is not fixed,
// so charging every non-block-part a flat overhead lets one identity retain
// orders of magnitude more than the ceiling admits: measured in the accounting
// it looks tiny, in memory it is gigabytes.
func TestPeerLanesByteCeilingBoundsLargePayloads(t *testing.T) {
	const byteCap = 8 << 20
	const extBytes = 64 << 10

	testCases := []struct {
		name string
		send func(peer types.NodeID) msgInfo
	}{
		{
			name: "precommits",
			send: func(peer types.NodeID) msgInfo {
				return msgInfo{Msg: &VoteMessage{Vote: bigPrecommitVote(types.MaxVoteExtensions, extBytes)}, PeerID: peer}
			},
		},
		{
			name: "commits",
			send: func(peer types.NodeID) msgInfo {
				return msgInfo{Msg: &CommitMessage{Commit: bigCommit(types.MaxVoteExtensions, extBytes)}, PeerID: peer}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			// The message count is left generous so it is the byte ceiling, not
			// the message count, that binds.
			lanes := newPeerLanes(withLaneByteCapacity(byteCap), withLaneBufferCapacity(1<<20))

			// A message larger than the whole ceiling is admitted alone rather than
			// refused, so its own retained size is the only slack the bound allows.
			oneMessage := oracleRoundedPayload(messageMarshaledSize(t, tc.send("sample").Msg))

			// Offer far more payload than the ceiling, spread across peers so no
			// single lane's own capacity is what bounds it.
			for i := 0; i < 64; i++ {
				peer := types.NodeID(fmt.Sprintf("peer-%d", i%8))
				require.NoError(t, lanes.send(ctx, tc.send(peer)))
			}

			require.LessOrEqual(t, retainedMessageBytes(t, lanes), byteCap+oneMessage,
				"large vote/commit payloads bypassed the byte ceiling")
		})
	}
}

// The byte ceiling must bound the heap a payload actually pins, including the
// slack the Go allocator adds when it rounds a slice up to a size class. proto.Size
// counts a slice by its length, but a slice one byte past a 32 KiB class is served
// from the next 8 KiB page, retaining ~25% more than its length. Charging only the
// length would let a flood of such messages pin that much more heap than the
// ceiling admits. The oracle rounds each payload up to the page the allocator
// gives it, so a queue that still fits proves the charge bounds the rounded heap,
// not merely the wire length.
func TestPeerLanesByteCeilingCoversAllocatorRounding(t *testing.T) {
	ctx := context.Background()
	const byteCap = 8 << 20
	// One byte past a 32 KiB size class, so the allocator rounds the payload up by a
	// whole page — the worst-case slack proto.Size does not see.
	const payload = (32 << 10) + 1
	lanes := newPeerLanes(withLaneByteCapacity(byteCap), withLaneBufferCapacity(1<<20))

	oneMessage := oracleRoundedPayload(messageMarshaledSize(t,
		&CommitMessage{Commit: quorumHashCommit(payload)}))

	// Offer far more payload than the ceiling, spread across peers so no single
	// lane's own capacity is what bounds it.
	for i := 0; i < 512; i++ {
		peer := types.NodeID(fmt.Sprintf("peer-%d", i%8))
		require.NoError(t, lanes.send(ctx, msgInfo{
			Msg:    &CommitMessage{Commit: quorumHashCommit(payload)},
			PeerID: peer,
		}))
	}

	require.LessOrEqual(t, retainedMessageBytes(t, lanes), byteCap+oneMessage,
		"large-slice payloads pinned more heap than the ceiling once allocator rounding is counted")
}

// Popping a message must release its payload from the lane's backing array, not
// merely advance the slice past it. A reslice with queue = queue[1:] leaves the
// popped element — and the large payload it points at — reachable through the
// backing array until the slice reallocates, so a lane would pin far more heap
// than its live messages account for. Clearing the slot before advancing is what
// keeps the retained heap no larger than the charge.
func TestPeerLanePoppedMessagesReleaseTheirBackingArraySlots(t *testing.T) {
	ctx := context.Background()
	lanes := newPeerLanes(withLaneBufferCapacity(1<<20), withLaneByteCapacity(1<<30))

	const peer = types.NodeID("filler")
	const n = 16
	for i := 0; i < n; i++ {
		require.NoError(t, lanes.send(ctx, msgInfo{
			Msg:    &CommitMessage{Commit: quorumHashCommit(64 << 10)},
			PeerID: peer,
		}))
	}

	lanes.mtx.Lock()
	defer lanes.mtx.Unlock()
	lane := lanes.lanes[peer]
	// A view over the whole backing array, so slots the live slice advances past
	// stay observable after the head pointer has moved beyond them.
	backing := lane.queue[:cap(lane.queue)]
	require.GreaterOrEqual(t, len(backing), n, "the lane must hold all the messages sent")

	for i := 0; i < n-1; i++ {
		lanes.popOldest(lane)
	}

	// Every popped slot must hold no message: its payload is released, not pinned
	// behind the live slice by the backing array.
	for i := 0; i < n-1; i++ {
		require.Nil(t, backing[i].Msg, "a popped message is still pinned by the lane's backing array")
	}
}

// messageMarshaledSize is the retained wire payload of one message, measured by
// actually marshaling it. It is deliberately a different implementation from the
// scheduler's proto.Size accounting: an oracle that reused the same call could
// only ever agree with the code it is meant to check.
func messageMarshaledSize(t *testing.T, msg Message) int {
	t.Helper()
	pb, err := MsgToProto(msg)
	require.NoError(t, err)
	bz, err := proto.Marshal(pb)
	require.NoError(t, err)
	return len(bz)
}

// oracleVoteExtensionHeap and oracleProofAuntHeap model, for the test oracle
// alone, the heap one deserialized repeated element retains on top of its wire
// bytes: the struct, slice headers and container pointer that live in memory but
// never travel the wire. They are deliberate under-estimates of the true Go
// object cost — a vote extension's three-plus slice headers alone are ~72 bytes,
// an aunt's header and container pointer ~32 — chosen below the scheduler's
// production surcharge and derived independently of it, so the oracle is a
// genuine second opinion. A queue whose retained heap, modelled this way, exceeds
// the ceiling proves the charge under-counted; a queue that stays within it
// proves the charge covers at least this much real memory.
const (
	oracleVoteExtensionHeap = 128
	oracleProofAuntHeap     = 32
)

// oracleGoPageBytes is the page the Go allocator serves large objects from: an
// allocation past 32 KiB is rounded up to a whole number of these pages, so the
// heap a slice pins is its length rounded up to this boundary. The oracle rounds
// each modeled payload this way so it counts the allocator slack the wire length
// omits rather than ignoring it.
const oracleGoPageBytes = 8 << 10

// oracleRoundedPayload rounds a marshaled payload size up to the page the Go
// allocator would serve it from, modelling the retained heap rather than the
// wire length. It is deliberately a different derivation from the scheduler's
// 13/10 scaling, so the two agree only if both really bound the rounded heap.
func oracleRoundedPayload(marshaled int) int {
	return (marshaled + oracleGoPageBytes - 1) / oracleGoPageBytes * oracleGoPageBytes
}

// retainedMessageBytes estimates the heap the lanes actually retain: every
// message's marshaled content — counted by really marshaling it, a different
// implementation from the scheduler's proto.Size so the two cross-check rather
// than agree by construction, and rounded up to the allocator page the runtime
// serves it from so the estimate is an upper bound on retained heap rather than a
// lower bound on wire length — plus a per-element estimate of the Go object
// overhead each deserialized repeated element carries beyond the wire. A complete
// serialization cannot omit a field, so it counts the bytes a hand-written
// enumeration forgets — a vote extension's sign_request_id, a commit's quorum
// hash, a block part's Merkle proof. The per-element term counts what the wire
// omits entirely: the slice headers and pointers a message of many near-empty
// extensions pins in memory while its wire size stays small.
func retainedMessageBytes(t *testing.T, lanes *peerLanes) int {
	t.Helper()
	lanes.mtx.Lock()
	defer lanes.mtx.Unlock()
	total := 0
	for _, lane := range lanes.lanes {
		for _, entry := range lane.queue {
			total += oracleRoundedPayload(messageMarshaledSize(t, entry.Msg)) + oracleElementHeap(entry.Msg)
		}
	}
	return total
}

// oracleElementHeap is the test oracle's independent estimate of the Go object
// overhead a message's repeated elements retain beyond their wire bytes. It walks
// the same fields the scheduler surcharges but applies its own constants, so it
// checks the charge rather than mirroring it.
func oracleElementHeap(msg Message) int {
	switch m := msg.(type) {
	case *VoteMessage:
		if m.Vote == nil {
			return 0
		}
		return oracleVoteExtensionHeap * len(m.Vote.VoteExtensions)
	case *CommitMessage:
		if m.Commit == nil {
			return 0
		}
		return oracleVoteExtensionHeap * len(m.Commit.ThresholdVoteExtensions)
	case *BlockPartMessage:
		if m.Part == nil {
			return 0
		}
		return oracleProofAuntHeap * len(m.Part.Proof.Aunts)
	}
	return 0
}

// signRequestIDPrecommit builds a precommit whose retained memory is dominated by
// vote-extension sign_request_id payloads — an attacker-controlled, unbounded
// field that a field-by-field byte count omits entirely.
func signRequestIDPrecommit(extensions, idBytes int) *types.Vote {
	exts := make([]*tmproto.VoteExtension, extensions)
	for i := range exts {
		exts[i] = &tmproto.VoteExtension{
			Type: tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW,
			XSignRequestId: &tmproto.VoteExtension_SignRequestId{
				SignRequestId: make([]byte, idBytes),
			},
		}
	}
	ve, err := types.VoteExtensionsFromProto(exts...)
	if err != nil {
		panic(err)
	}
	return &types.Vote{
		Type:           tmproto.PrecommitType,
		Height:         1,
		VoteExtensions: ve,
	}
}

// quorumHashCommit builds a commit whose retained memory is dominated by its
// quorum hash — a variable-length field the field-by-field count omits.
func quorumHashCommit(hashBytes int) *types.Commit {
	return &types.Commit{Height: 1, QuorumHash: make([]byte, hashBytes)}
}

// auntsBlockPart builds a block part whose retained memory is dominated by its
// Merkle proof's aunts and leaf hash — fields the field-by-field count omits,
// charging the part only for its (here tiny) data bytes.
func auntsBlockPart(auntCount, auntBytes int) *types.Part {
	aunts := make([][]byte, auntCount)
	for i := range aunts {
		aunts[i] = make([]byte, auntBytes)
	}
	return &types.Part{
		Index: 0,
		Bytes: []byte{0x01},
		Proof: merkle.Proof{
			Total:    1,
			Index:    0,
			LeafHash: make([]byte, auntBytes),
			Aunts:    aunts,
		},
	}
}

// The shared byte ceiling must bound the memory a message really pins, whatever
// field the bytes sit in. A field-by-field byte count is only ever as complete
// as the last person to update it remembered to make it: it charges a near-1 MiB
// message that hides its payload in a vote extension's sign_request_id, a
// commit's quorum hash, or a block part's Merkle proof for its fixed overhead
// alone, so one lane retains hundreds of megabytes under a 64 MiB ceiling. The
// charge must instead be a complete measure of the message — its marshaled size —
// which cannot forget a field.
func TestPeerLanesByteCeilingCountsAllRetainedFields(t *testing.T) {
	const byteCap = 8 << 20

	testCases := []struct {
		name string
		make func() Message
	}{
		{
			name: "vote extension sign_request_id",
			make: func() Message {
				return &VoteMessage{Vote: signRequestIDPrecommit(1, 1<<20)}
			},
		},
		{
			name: "commit quorum hash",
			make: func() Message {
				return &CommitMessage{Commit: quorumHashCommit(1 << 20)}
			},
		},
		{
			name: "block part proof aunts",
			make: func() Message {
				return &BlockPartMessage{Height: 1, Part: auntsBlockPart(16, 64<<10)}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			// The message count is left generous so it is the byte ceiling, not the
			// message count, that binds.
			lanes := newPeerLanes(withLaneByteCapacity(byteCap), withLaneBufferCapacity(1<<20))

			// The most one message can pin: a message larger than the whole ceiling
			// is admitted alone rather than refused, so its own size is the only
			// slack the bound allows.
			oneMessage := messageMarshaledSize(t, tc.make())
			require.Less(t, oneMessage, byteCap, "one message must fit under the ceiling for the test to bind")

			// Offer far more payload than the ceiling, spread across peers so no
			// single lane's own capacity is what bounds it.
			for i := 0; i < 64; i++ {
				peer := types.NodeID(fmt.Sprintf("peer-%d", i%8))
				require.NoError(t, lanes.send(ctx, msgInfo{Msg: tc.make(), PeerID: peer}))
			}

			require.LessOrEqual(t, retainedMessageBytes(t, lanes), byteCap+oneMessage,
				"an attacker-controlled field bypassed the byte ceiling")
		})
	}
}

// The byte ceiling must bound the heap a message really pins, and a message pins
// more than its wire size. Every vote extension it declares is held as a separate
// Go object — a struct, its slice headers, a container pointer — that proto.Size,
// measuring the wire form, never sees. Charging only the wire size lets a peer
// declaring many near-empty extensions sit under the ceiling in the accounting
// while retaining several times it in memory. The charge must surcharge each
// element, so a queue of maximum-extension messages stays within the ceiling in
// actual retained terms, not just in wire terms.
func TestPeerLanesByteCeilingBoundsPerElementHeap(t *testing.T) {
	ctx := context.Background()
	const byteCap = 1 << 20

	lanes := newPeerLanes(withLaneByteCapacity(byteCap), withLaneBufferCapacity(1<<20))

	msg := func() Message { return &VoteMessage{Vote: bigPrecommitVote(types.MaxVoteExtensions, 0)} }
	// The most one message can pin, in retained terms: a message larger than the
	// whole ceiling is admitted alone, so its own retained size is the only slack.
	oneMessage := messageMarshaledSize(t, msg()) + oracleElementHeap(msg())

	// Offer far more of these near-empty-extension messages than the ceiling could
	// hold once their per-element heap is counted, spread across peers so no single
	// lane's own capacity is what bounds the queue. Their wire size is tiny, so a
	// charge that saw only the wire would admit thousands and pin megabytes.
	for i := 0; i < 2048; i++ {
		peer := types.NodeID(fmt.Sprintf("peer-%d", i%16))
		require.NoError(t, lanes.send(ctx, msgInfo{Msg: msg(), PeerID: peer}))
	}

	require.LessOrEqual(t, retainedMessageBytes(t, lanes), byteCap+oneMessage,
		"a message of many near-empty vote extensions retained more heap than the ceiling allows")
}

// Consensus messages are only valid for the height and round they were made in.
// A lane at capacity therefore gives up its oldest message: keeping those and
// refusing what just arrived would spend every turn on messages that are
// guaranteed stale and mute the peer for as long as it stays busy.
func TestPeerLanesOverflowKeepsTheFreshestMessages(t *testing.T) {
	ctx := context.Background()
	lanes := newPeerLanes()

	for i := 0; i < laneCapacity; i++ {
		mi := cheapLaneMsg("peer")
		mi.Msg.(*VoteMessage).Vote.Round = int32(i)
		require.NoError(t, lanes.send(ctx, mi))
	}
	fresh := cheapLaneMsg("peer")
	fresh.Msg.(*VoteMessage).Vote.Round = int32(laneCapacity)
	require.NoError(t, lanes.send(ctx, fresh))

	served := drainLanes(t, lanes)
	require.Len(t, served, laneCapacity, "the lane must not grow past its capacity")
	require.Equal(t, int32(laneCapacity), served[len(served)-1].Msg.(*VoteMessage).Vote.Round,
		"the message that arrived last must be delivered")
	require.Equal(t, int32(1), served[0].Msg.(*VoteMessage).Vote.Round,
		"the message that had waited longest must be the one dropped")
}

// Shedding is a local decision about this node's capacity, so it must not
// surface as an error: the reactor turns any error from delivering a peer
// message into a peer error, and the peer that fills its lane is the one
// sending as fast as we can accept.
func TestPeerLanesOverflowIsNotReportedAsAnError(t *testing.T) {
	ctx := context.Background()
	lanes := newPeerLanes()

	for i := 0; i < 4*laneCapacity; i++ {
		require.NoError(t, lanes.send(ctx, cheapLaneMsg("peer")),
			"a full lane must shed silently, never report the sender")
	}
}

// One peer's backlog must not evict another's messages before the node is
// genuinely out of room, and when it is, the slot comes from whoever is using
// most of it.
func TestPeerLanesAggregateCapacityShedsFromTheLongestLane(t *testing.T) {
	ctx := context.Background()

	// Shrink the shared bound for the test; the ratio to laneCapacity is what
	// matters, not the absolute number.
	const quiet = 8
	const bufferCapacity = laneCapacity + quiet
	lanes := newPeerLanes(withLaneBufferCapacity(bufferCapacity))

	for i := 0; i < laneCapacity; i++ {
		require.NoError(t, lanes.send(ctx, cheapLaneMsg("hog")))
	}
	for i := 0; i < quiet; i++ {
		require.NoError(t, lanes.send(ctx, cheapLaneMsg("quiet")))
	}
	require.Equal(t, bufferCapacity, lanes.buffered())

	// The node is now out of room. The next message must cost the hog a slot,
	// not the peer with the short lane.
	require.NoError(t, lanes.send(ctx, cheapLaneMsg("quiet")))
	require.Equal(t, laneCapacity-1, len(lanes.lanes["hog"].queue))
	require.Equal(t, quiet+1, len(lanes.lanes["quiet"].queue))
}

// The lanes together must not hold more than the single queue they replace, so
// per-peer scheduling cannot raise the memory a flood of block parts pins.
func TestPeerLaneCapacityDoesNotRaiseTheMemoryCeiling(t *testing.T) {
	require.Equal(t, msgQueueSize, newPeerLanes().bufferCapacity)
}

// A node catching up from a single peer receives everything from that one lane,
// so the capacity must cover what one peer legitimately delivers at once: every
// part of a maximum-size proposal block, and a full per-peer vote burst of the
// cheapest messages.
func TestPeerLaneCapacityCoversCatchUpFromOnePeer(t *testing.T) {
	blockParts := int(types.DefaultConsensusParams().Block.MaxBytes/int64(types.BlockPartSizeBytes)) + 1
	assert.GreaterOrEqual(t, laneCapacity, blockParts,
		"a node syncing from one peer must be able to hold a whole proposal block's parts")
	assert.GreaterOrEqual(t, laneCapacity, voteRateBurst/baseMessageCost,
		"a peer's whole instantaneous vote allowance must fit in its lane")
}

// A disconnected peer's lane goes away with it: what it queued can no longer
// help us make progress, and the lane must stop taking turns.
func TestPeerLanesPurgeOnPeerDown(t *testing.T) {
	ctx := context.Background()
	lanes := newPeerLanes()

	for i := 0; i < 10; i++ {
		require.NoError(t, lanes.send(ctx, cheapLaneMsg("gone")))
		require.NoError(t, lanes.send(ctx, cheapLaneMsg("here")))
	}

	lanes.purgePeer("gone")
	require.NotContains(t, lanes.lanes, types.NodeID("gone"))
	require.Equal(t, 10, lanes.buffered())

	served := drainLanes(t, lanes)
	require.Len(t, served, 10)
	for _, mi := range served {
		require.Equal(t, types.NodeID("here"), mi.PeerID, "a purged peer must not be served")
	}

	// The same peer reconnecting starts over rather than inheriting anything.
	require.NoError(t, lanes.send(ctx, cheapLaneMsg("gone")))
	served = drainLanes(t, lanes)
	require.Len(t, served, 1)
	require.Equal(t, types.NodeID("gone"), served[0].PeerID)
}

// A message a peer left in flight when its connection ended must not create or
// revive a lane once the peer is gone. The count of active lanes is meant to be
// bounded by the connection slots the node accepts; a stale send that
// resurrects a lane for a departed peer breaks that bound. Binding a lane to the
// session its connection was admitted under closes it: a purge ends the session,
// and a message carrying an ended session is dropped without touching a lane.
func TestPeerLanesSessionGatesStaleSends(t *testing.T) {
	ctx := context.Background()
	lanes := newPeerLanes()

	session := lanes.admit("peer")
	live := ctxWithPeerLaneSession(ctx, session)
	require.NoError(t, lanes.send(live, cheapLaneMsg("peer")))
	require.Contains(t, lanes.lanes, types.NodeID("peer"))

	// The peer disconnects: its lane and its session are retired together.
	lanes.purgePeer("peer")
	require.NotContains(t, lanes.lanes, types.NodeID("peer"))

	// A message the departed connection left in flight cannot bring the lane back.
	require.NoError(t, lanes.send(live, cheapLaneMsg("peer")))
	require.NotContains(t, lanes.lanes, types.NodeID("peer"),
		"a send from an ended session recreated a lane for a departed peer")

	// The same node reconnecting is admitted under a new session. A straggler
	// from the old session is still refused; the new session's messages are not.
	session2 := lanes.admit("peer")
	require.NotEqual(t, session, session2)
	require.NoError(t, lanes.send(live, cheapLaneMsg("peer")))
	require.NotContains(t, lanes.lanes, types.NodeID("peer"),
		"a straggler from the old session was admitted into the reconnected peer's lane")
	require.NoError(t, lanes.send(ctxWithPeerLaneSession(ctx, session2), cheapLaneMsg("peer")))
	require.Contains(t, lanes.lanes, types.NodeID("peer"))
}

// A peer message that carries no session — a path that predates connection
// sessions, or this node's own work — keeps the former admission behaviour so
// the gate cannot silently drop traffic that never opted into it.
func TestPeerLanesAdmitSessionlessSends(t *testing.T) {
	ctx := context.Background()
	lanes := newPeerLanes()

	require.NoError(t, lanes.send(ctx, cheapLaneMsg("peer")))
	require.Contains(t, lanes.lanes, types.NodeID("peer"))
}

// deliverForConn mirrors the reactor's per-message decision: derive the lane
// session from the immutable connection generation the envelope carries, and drop
// the message when that generation is no longer the peer's live connection. It is
// the exact logic of the handlers' laneCtx, exercised against the real peer state
// and lanes.
func deliverForConn(t *testing.T, lanes *peerLanes, ps *PeerState, connID uint64) {
	t.Helper()
	session, ok := ps.laneSessionForConn(connID)
	if !ok {
		return
	}
	require.NoError(t, lanes.send(ctxWithPeerLaneSession(context.Background(), session), cheapLaneMsg("peer")))
}

// The killer sequence the connection generation closes: an envelope a connection
// left in flight must not inherit the session of a later connection that
// reconnected under the same NodeID. The reactor used to read the peer state's
// current session at message-handling time, so a message buffered across a
// reconnect was stamped with — and admitted under — the new connection's session.
// Deriving the session from the generation the envelope carried at ingress
// refuses it instead.
func TestPeerLanesStaleEnvelopeCannotInheritReconnectSession(t *testing.T) {
	lanes := newPeerLanes()
	ps := NewPeerState(log.NewNopLogger(), "peer")

	// 1. Connection 1 comes up: admitted under session s1, generation g1. It puts
	//    an envelope stamped g1 in flight (held, as it would sit in the router's
	//    shared inbound queue).
	const g1 = 1
	s1 := lanes.admit("peer")
	ps.SetLaneAdmission(g1, s1)

	// 2-3. Connection 1 goes down — its lane and session are purged — and the same
	//      NodeID reconnects as connection 2: session s2, generation g2.
	lanes.purgePeer("peer")
	const g2 = 2
	s2 := lanes.admit("peer")
	ps.SetLaneAdmission(g2, s2)
	require.NotEqual(t, s1, s2)

	// 4-5. The envelope connection 1 left in flight is finally handled. Derived
	//      from its own generation g1, it is refused rather than admitted under
	//      connection 2's live session.
	deliverForConn(t, lanes, ps, g1)
	require.NotContains(t, lanes.lanes, types.NodeID("peer"),
		"a stale envelope from an ended connection inherited the reconnect's session")

	// A live envelope from connection 2 is admitted as normal.
	deliverForConn(t, lanes, ps, g2)
	require.Contains(t, lanes.lanes, types.NodeID("peer"),
		"a live envelope from the current connection must still be admitted")
}

// A repeated up for a connection that never went down — a validator-set update
// re-announcing a live peer — must not strand the messages already in flight.
// The generation is stable across such an up, so a message carrying it still
// matches and is admitted.
func TestPeerLanesRepeatedUpWithoutDownIsIdempotent(t *testing.T) {
	lanes := newPeerLanes()
	ps := NewPeerState(log.NewNopLogger(), "peer")

	const g1 = 1
	s1 := lanes.admit("peer")
	ps.SetLaneAdmission(g1, s1)

	// The same connection is announced again: admit returns the same session, and
	// the generation is unchanged.
	s1again := lanes.admit("peer")
	require.Equal(t, s1, s1again, "a live connection keeps its session across a repeated up")
	ps.SetLaneAdmission(g1, s1again)

	deliverForConn(t, lanes, ps, g1)
	require.Contains(t, lanes.lanes, types.NodeID("peer"),
		"a repeated up for the same connection stranded a message already in flight")
}

// Node identities are free to mint, so an attacker can connect, send one
// message and disconnect for as long as it likes.
//
// The rotation may never grow with the identities it burns through: only a lane
// with something waiting takes a turn, so a served identity leaves at once. The
// lane table is bounded more loosely — a lane is dated when it falls idle and
// reclaimed on the next sweep — so it holds what one sweep interval's worth of
// churn leaves behind, and no more.
func TestPeerLanesIdentityRotationIsBounded(t *testing.T) {
	ctx := context.Background()
	clock := clockwork.NewFakeClock()
	lanes := newPeerLanes(withLaneClock(clock))

	const identities = 500
	require.NoError(t, lanes.send(ctx, cheapLaneMsg("honest")))
	for i := 0; i < identities; i++ {
		require.NoError(t, lanes.send(ctx, cheapLaneMsg(types.NodeID(fmt.Sprintf("throwaway-%d", i)))))
		_, ok := lanes.next()
		require.True(t, ok)
		require.LessOrEqual(t, len(lanes.rotation), 2,
			"only lanes with messages waiting may take a turn in the rotation")
	}

	// Every identity is gone from the rotation as soon as its message is served,
	// and out of the lane table once it has been idle long enough.
	drainLanes(t, lanes)
	require.Empty(t, lanes.rotation)
	clock.Advance(laneIdleTimeout + time.Second)
	require.NoError(t, lanes.send(ctx, cheapLaneMsg("honest")))
	require.Len(t, lanes.lanes, 1, "idle lanes must be reclaimed")
	require.Contains(t, lanes.lanes, types.NodeID("honest"))
}

// A lane is reclaimed only once it has been idle, never while its peer still
// has messages waiting.
func TestPeerLanesReclaimSparesBusyLanes(t *testing.T) {
	ctx := context.Background()
	clock := clockwork.NewFakeClock()
	lanes := newPeerLanes(withLaneClock(clock))

	require.NoError(t, lanes.send(ctx, cheapLaneMsg("busy")))
	require.NoError(t, lanes.send(ctx, cheapLaneMsg("idle")))
	drainLanes(t, lanes)
	require.NoError(t, lanes.send(ctx, cheapLaneMsg("busy")))

	clock.Advance(laneIdleTimeout + time.Second)
	_, ok := lanes.next()
	require.True(t, ok)

	require.Contains(t, lanes.lanes, types.NodeID("busy"),
		"a lane with a message waiting must survive reclamation")
	require.NotContains(t, lanes.lanes, types.NodeID("idle"),
		"a lane with nothing waiting must be reclaimed once it has been idle")
}

// Lanes are filled by the reactor's per-channel goroutines, drained by the
// scheduler, and retired by the peer-update goroutine — none of them ordered
// against the others, so a peer's lane can be purged while its last messages are
// still arriving and while the rotation is being served.
func TestPeerLanesTolerateConcurrentSendServeAndPurge(t *testing.T) {
	ctx := context.Background()
	lanes := newPeerLanes()

	const peers = 8
	const perPeer = 200

	var producers, servers sync.WaitGroup
	stop := make(chan struct{})
	failures := make(chan error, 2*peers)

	for i := 0; i < peers; i++ {
		peerID := types.NodeID(fmt.Sprintf("peer-%d", i))
		producers.Add(2)
		go func() {
			defer producers.Done()
			for j := 0; j < perPeer; j++ {
				if err := lanes.send(ctx, laneMsg(peerID, j%3)); err != nil {
					failures <- err
					return
				}
			}
		}()
		go func() {
			defer producers.Done()
			for j := 0; j < perPeer/10; j++ {
				lanes.purgePeer(peerID)
			}
		}()
	}
	servers.Add(2)
	go func() {
		defer servers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			lanes.next()
		}
	}()
	go func() {
		defer servers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			lanes.buffered()
		}
	}()

	producers.Wait()
	close(stop)
	servers.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}

	// Whatever the interleaving, the accounting still adds up: what the lanes say
	// they hold is what they hold, and only lanes with messages take turns.
	require.Equal(t, lanes.buffered(), countQueued(lanes))
	require.Len(t, lanes.rotation, countActiveLanes(lanes))
}

func countQueued(lanes *peerLanes) int {
	lanes.mtx.Lock()
	defer lanes.mtx.Unlock()
	total := 0
	for _, lane := range lanes.lanes {
		total += len(lane.queue)
	}
	return total
}

func countActiveLanes(lanes *peerLanes) int {
	lanes.mtx.Lock()
	defer lanes.mtx.Unlock()
	active := 0
	for _, lane := range lanes.lanes {
		if lane.active() {
			active++
		}
	}
	return active
}

// Making room in the budget for a message is only sound if what the scheduler
// reads from the budget already accounts for everything dispatched. Since the
// charges are made while the message is verified, the scheduler must hand over
// one peer message at a time and wait to be told it is finished. Without that,
// two messages read the same tokens and spend them twice.
func TestPeerLanesHandOverOnePeerMessageAtATime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	lanes := newPeerLanes()
	require.NoError(t, lanes.send(ctx, cheapLaneMsg("peer")))
	require.NoError(t, lanes.send(ctx, cheapLaneMsg("peer")))

	first, ok := lanes.recv(ctx)
	require.True(t, ok)
	require.Equal(t, types.NodeID("peer"), first.PeerID)

	blocked := make(chan msgInfo, 1)
	go func() {
		mi, ok := lanes.recv(ctx)
		if ok {
			blocked <- mi
		}
	}()
	select {
	case <-blocked:
		t.Fatal("a second peer message was handed over before the first was finished")
	case <-time.After(50 * time.Millisecond):
	}

	lanes.settle()
	select {
	case mi := <-blocked:
		require.Equal(t, types.NodeID("peer"), mi.PeerID)
	case <-ctx.Done():
		t.Fatal("the scheduler did not resume once the message was reported finished")
	}
}

// This node's own messages are not handed over one at a time: they are neither
// charged nor reported, so waiting for a report that never comes would stop the
// rotation for good.
func TestPeerLanesDoNotWaitForLocalMessagesToBeReported(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	lanes := newPeerLanes()
	require.NoError(t, lanes.send(ctx, cheapLaneMsg("")))
	require.NoError(t, lanes.send(ctx, cheapLaneMsg("")))

	for i := 0; i < 2; i++ {
		mi, ok := lanes.recv(ctx)
		require.True(t, ok, "the rotation stopped waiting for a report on a local message")
		require.Equal(t, types.NodeID(""), mi.PeerID)
	}
}

// The scheduler runs on the queue reader's goroutine, whose shutdown path the
// consensus goroutine waits for. A wait that ignored cancellation would keep
// the node from stopping.
func TestPeerLanesRecvReturnsOnContextCancel(t *testing.T) {
	t.Run("idle", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		lanes := newPeerLanes()

		returned := make(chan bool, 1)
		go func() {
			_, ok := lanes.recv(ctx)
			returned <- ok
		}()
		cancel()
		select {
		case ok := <-returned:
			require.False(t, ok)
		case <-time.After(5 * time.Second):
			t.Fatal("the scheduler did not return after cancellation")
		}
	})

	t.Run("waiting for budget", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		clock := clockwork.NewFakeClock()
		budget := newVerificationBudget(300, withVerificationBudgetClock(clock))
		drainVerificationBudget(budget)
		lanes := newPeerLanes(withLaneBudget(budget), withLaneClock(clock))
		require.NoError(t, lanes.send(ctx, expensiveLaneMsg("peer", 4)))

		returned := make(chan bool, 1)
		go func() {
			_, ok := lanes.recv(ctx)
			returned <- ok
		}()
		require.NoError(t, clock.BlockUntilContext(ctx, 1), "the message must be waiting for budget")

		cancel()
		select {
		case ok := <-returned:
			require.False(t, ok)
		case <-time.After(5 * time.Second):
			t.Fatal("the scheduler did not return while waiting for verification budget")
		}
	})
}

// This node's own messages carry no peer, are not charged, and must never be
// held up by a budget that exists to bound what peers can force.
func TestPeerLanesDoNotChargeLocalMessages(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	clock := clockwork.NewFakeClock()
	budget := newVerificationBudget(300, withVerificationBudgetClock(clock))
	drainVerificationBudget(budget)
	lanes := newPeerLanes(withLaneBudget(budget), withLaneClock(clock))

	local := expensiveLaneMsg("", 4)
	require.NoError(t, lanes.send(ctx, local))

	// The clock never advances, so anything that waited for budget would hang.
	mi, ok := lanes.recv(ctx)
	require.True(t, ok)
	require.Equal(t, types.NodeID(""), mi.PeerID)
}

// A lane that saves up for an expensive head must not keep the credit when that
// head is shed.
//
// The deficit a lane accumulates is granted for the message at its head: it is
// what lets a lane wait out the turns an expensive message costs instead of
// being overtaken by cheaper ones forever. Shedding drops that head without
// serving it, so credit left behind is credit for work the node never did — and
// a peer can arrange it deliberately by sending one expensive message and then
// filling its own lane until the head is shed, turning the wait into a burst of
// cheap messages served out of turn.
func TestPeerLanesShedHeadTakesItsDeficitWithIt(t *testing.T) {
	ctx := context.Background()
	// Room enough that only the per-lane bound sheds anything.
	lanes := newPeerLanes(withLaneBufferCapacity(4 * laneCapacity))

	head := expensiveLaneMsg("saver", types.MaxVoteExtensions)
	headCost := laneTurnCost(head)
	require.Greater(t, headCost, laneQuantum,
		"the head must cost more than one turn grants, or there is nothing to save up for")

	require.NoError(t, lanes.send(ctx, head))
	// Another lane keeps the rotation turning while the expensive head waits.
	for i := 0; i < 2*laneCapacity; i++ {
		require.NoError(t, lanes.send(ctx, cheapLaneMsg("other")))
	}
	for i := 0; i < headCost-1; i++ {
		mi, ok := lanes.next()
		require.True(t, ok)
		require.Equal(t, types.NodeID("other"), mi.PeerID,
			"a head costing more than its lane has been granted must yield")
	}
	require.Equal(t, headCost-1, lanes.lanes["saver"].deficit,
		"the waiting lane must have accumulated the credit its head needs")

	// The peer now fills its own lane until the head it saved up for is shed.
	for i := 0; i < laneCapacity; i++ {
		require.NoError(t, lanes.send(ctx, cheapLaneMsg("saver")))
	}
	require.Equal(t, baseMessageCost, laneTurnCost(lanes.lanes["saver"].queue[0].msgInfo),
		"the expensive head must have been shed, leaving a cheap message at the head")

	// Measure one contiguous turn of the lane that did the saving.
	burst := 0
	for {
		mi, ok := lanes.next()
		require.True(t, ok)
		if mi.PeerID == "saver" {
			burst = laneTurnCost(mi)
			break
		}
	}
	for {
		mi, ok := lanes.next()
		require.True(t, ok)
		if mi.PeerID != "saver" {
			break
		}
		burst += laneTurnCost(mi)
	}

	assert.LessOrEqual(t, burst, laneQuantum,
		"a shed head left its lane credit to spend on messages served out of turn")
}

// Room taken from a lane to make space for another peer's message must not turn
// the credit that lane saved into a burst — and must not silence the lane
// either.
//
// A lane accumulates credit one quantum at a time, granted for the message at
// its head, so an expensive head needs as many turns as it costs. Shedding that
// head from across lanes would strand the saved credit on a message the node
// never served: the peer could have a colluder flood the shared bound, evict its
// expensive head, and spend the saved credit on a burst of cheap messages served
// out of turn. Resetting the credit instead would silence the lane — an
// expensive head is exactly what makes a lane the longest, so its heaviest
// messages would be shed and never served. Taking the newest instead keeps the
// head and its credit paired, throttling the lane without either failure.
func TestPeerLanesCrossLaneShedCannotFundABurst(t *testing.T) {
	ctx := context.Background()
	// Small shared bound so the next arrival forces a cross-lane shed.
	const bufferCapacity = 8
	lanes := newPeerLanes(withLaneBufferCapacity(bufferCapacity))

	// The victim's lane: an expensive head with cheap messages behind it, so a
	// shed that dropped the head would leave cheap messages plus the credit saved
	// for the head — exactly the burst to prevent.
	head := expensiveLaneMsg("victim", 4)
	headCost := laneTurnCost(head)
	require.Greater(t, headCost, laneQuantum)
	require.NoError(t, lanes.send(ctx, head))
	for i := 0; i < 4; i++ {
		require.NoError(t, lanes.send(ctx, cheapLaneMsg("victim")))
	}
	require.NoError(t, lanes.send(ctx, cheapLaneMsg("other")))

	// Let the victim's lane save up credit for its expensive head, one quantum a
	// turn, while another lane keeps the rotation turning.
	for lanes.lanes["victim"].deficit < headCost-1 {
		mi, ok := lanes.next()
		require.True(t, ok)
		if mi.PeerID == "other" {
			require.NoError(t, lanes.send(ctx, cheapLaneMsg("other")))
		}
	}
	saved := lanes.lanes["victim"].deficit
	require.Equal(t, headCost-1, saved,
		"the victim must have saved credit for its head, not yet enough to serve it")
	require.Equal(t, headCost, laneTurnCost(lanes.lanes["victim"].queue[0].msgInfo),
		"the expensive head must still be waiting")

	// Another peer floods the shared bound, so room is taken from the longest
	// lane — the victim's.
	for lanes.buffered() < bufferCapacity {
		require.NoError(t, lanes.send(ctx, cheapLaneMsg("other")))
	}
	before := len(lanes.lanes["victim"].queue)
	require.NoError(t, lanes.send(ctx, cheapLaneMsg("other")))
	require.Less(t, len(lanes.lanes["victim"].queue), before,
		"the test must actually shed from the lane that did the saving")

	// The victim's expensive head, and so the credit saved for it, survives the
	// cross-lane shed; the credit is not reset either.
	require.Equal(t, headCost, laneTurnCost(lanes.lanes["victim"].queue[0].msgInfo),
		"a cross-lane shed dropped the victim's head, stranding the credit saved for it")
	assert.Equal(t, saved, lanes.lanes["victim"].deficit,
		"a lane must keep the credit it earned when another peer takes its room")

	// When the victim is finally served, its first message is the expensive head
	// it saved for — not a cheap message funded by credit meant for the head.
	var first msgInfo
	for {
		mi, ok := lanes.next()
		require.True(t, ok)
		if mi.PeerID == "victim" {
			first = mi
			break
		}
	}
	assert.Equal(t, headCost, laneTurnCost(first),
		"the victim spent credit saved for its head on a message served out of turn")
}

// allowOnlyBudget is a verification budget that can decide affordability but
// cannot defer a message until its whole cost is affordable — the shape of a
// replacement budget that satisfies the public types.VerificationBudget contract
// yet lacks the wait the scheduler relies on.
type allowOnlyBudget struct{}

func (allowOnlyBudget) Allow(int) bool { return true }

// A budget the scheduler cannot wait on would let it admit a message on a
// preflight that then cannot cover the staged verification, breaking
// whole-message atomicity — the message pays for part of its work and is dropped
// after. Because an external caller can satisfy the public
// types.VerificationBudget contract with exactly this shape, the incompatibility
// must be reported as a construction error rather than a panic: budgetCanWait is
// the contract NewState enforces, and the bundled budget satisfies it while
// allowOnlyBudget does not.
func TestBudgetThatCannotWaitIsRejectedAtConstruction(t *testing.T) {
	require.False(t, budgetCanWait(allowOnlyBudget{}),
		"a budget that only decides affordability must be reported as unable to wait")
	require.True(t, budgetCanWait(newVerificationBudget(300)),
		"the bundled budget must satisfy the wait contract")
	require.True(t, budgetCanWait(nil),
		"a nil budget disables waiting and is not an error")

	// A budget the scheduler cannot wait on leaves it without a waiter rather than
	// silently taking effect, so the construction check is the only thing standing
	// between such a budget and lost atomicity.
	lanes := newPeerLanes(withLaneBudget(allowOnlyBudget{}))
	require.Nil(t, lanes.waiter,
		"an incompatible budget must not be installed as the scheduler's waiter")
}
