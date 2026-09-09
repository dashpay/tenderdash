package consensus

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/merkle"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

// The part-set caps bound how many parts a node ACCEPTS, and say nothing about
// the ones it rejects: a proof that does not check out leaves the slot empty, so
// the next copy is hashed all over again. Hashing a 64 kB leaf per attempt, for
// as long as a peer cares to send them, is work nothing charged for.
func TestRepeatedInvalidBlockPartProofsAreBounded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	clock := clockwork.NewFakeClock()
	action := &AddProposalBlockPartAction{
		logger:          log.NewNopLogger(),
		metrics:         NopMetrics(),
		statsQueue:      newChanQueue[msgInfo](),
		partProofBudget: newBlockPartProofBudget(withBlockPartProofClock(clock)),
	}
	stateData := partSetStateData(1)

	// Everything within the burst is verified and rejected on its merits.
	verified := 0
	for {
		msg := &BlockPartMessage{Height: 1, Round: 0, Part: invalidProofPart(0)}
		_, err := action.addProposalBlockPart(ctx, nil, stateData, msg, "peer", false)
		if err == nil {
			break
		}
		require.ErrorIs(t, err, types.ErrPartSetInvalidProof)
		verified++
		require.Less(t, verified, 1000, "an invalid proof was re-hashed without bound")
	}

	assert.LessOrEqual(t, verified*int(types.BlockPartSizeBytes), blockPartProofBurstBytes,
		"no more than a burst of leaf hashing may be spent on one peer's bad proofs")

	// Refusal is a local drop, not a verdict: nothing is reported, and the
	// allowance comes back on its own.
	clock.Advance(time.Minute)
	msg := &BlockPartMessage{Height: 1, Round: 0, Part: invalidProofPart(0)}
	_, err := action.addProposalBlockPart(ctx, nil, stateData, msg, "peer", false)
	require.ErrorIs(t, err, types.ErrPartSetInvalidProof,
		"the allowance must refill so a peer is never muted for good")
}

// Replayed parts are this node's own history, not a peer's demand. The
// write-ahead log is consumed far faster than the allowance refills, so
// charging it would let a run of failures recorded during normal operation
// throttle the replay of the valid parts that followed — and the node would
// fail to rebuild a block it had already assembled.
func TestBlockPartProofBudgetIgnoresReplayedParts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	clock := clockwork.NewFakeClock()
	action := &AddProposalBlockPartAction{
		logger:          log.NewNopLogger(),
		metrics:         NopMetrics(),
		statsQueue:      newChanQueue[msgInfo](),
		partProofBudget: newBlockPartProofBudget(withBlockPartProofClock(clock)),
	}
	stateData := partSetStateData(1)

	// Far more failures than any allowance covers, with no time passing.
	for i := 0; i < 500; i++ {
		msg := &BlockPartMessage{Height: 1, Round: 0, Part: invalidProofPart(0)}
		_, err := action.addProposalBlockPart(ctx, nil, stateData, msg, "peer", true)
		require.ErrorIs(t, err, types.ErrPartSetInvalidProof,
			"a replayed part must always be verified, however many failed before it")
	}
}

// A peer whose parts do check out is not the one this bounds. Once it catches
// up, each accepted part pays for the next, so its valid parts flow at full
// rate again — otherwise a peer that spent its allowance while its view of our
// part set was behind would have most of the parts we actually need refused,
// and the sender records each as delivered and never resends it.
func TestAcceptedBlockPartPaysForTheNextOne(t *testing.T) {
	clock := clockwork.NewFakeClock()
	budget := newBlockPartProofBudget(withBlockPartProofClock(clock))
	const peer = types.NodeID("peer")
	size := int(types.BlockPartSizeBytes)

	for budget.allow(peer, false, size) {
		budget.chargeFailure(peer, false, size)
	}
	require.False(t, budget.allow(peer, false, size), "the allowance must be spent")

	// One refill interval buys the first part back.
	clock.Advance(time.Second)
	require.True(t, budget.allow(peer, false, size))

	// From there the peer's valid parts sustain themselves with no time passing
	// at all: what a verified part cost is credited back.
	for i := 0; i < 100; i++ {
		require.True(t, budget.allow(peer, false, size),
			"a peer sending parts that verify must not be throttled")
		budget.accepted(peer, false, size)
	}
}

// The credit must not be farmable: a peer that interleaves valid parts with bad
// ones may buy exactly the part it earned, never a fresh burst. Restoring the
// whole allowance would let one valid part fund another 32 wasted leaf hashes.
func TestAcceptedBlockPartDoesNotRestoreTheWholeAllowance(t *testing.T) {
	clock := clockwork.NewFakeClock()
	budget := newBlockPartProofBudget(withBlockPartProofClock(clock))
	const peer = types.NodeID("peer")
	size := int(types.BlockPartSizeBytes)

	for budget.allow(peer, false, size) {
		budget.chargeFailure(peer, false, size)
	}

	// One valid part lands. It must buy one bad proof, and then no more.
	budget.accepted(peer, false, size)
	require.True(t, budget.allow(peer, false, size))
	budget.chargeFailure(peer, false, size)
	assert.False(t, budget.allow(peer, false, size),
		"a valid part must not fund a fresh burst of wasted hashing")
}

// This node's own parts are not what this bounds.
func TestBlockPartProofBudgetIgnoresLocalParts(t *testing.T) {
	budget := newBlockPartProofBudget()
	size := int(types.BlockPartSizeBytes)
	for i := 0; i < 1000; i++ {
		budget.chargeFailure("", false, size)
	}
	assert.True(t, budget.allow("", false, size), "this node's own parts are never charged")
}

// The per-peer allowance is what gives the node-wide bound its number: every
// connection slot may waste at most its own rate, so the hashing a flood can
// force across all of them stays a small fraction of one core.
func TestBlockPartProofBudgetNodeWideBound(t *testing.T) {
	const (
		connectionSlots = 68
		// A conservative floor for SHA-256 on the hardware this is expected to
		// run on. The real bound is better; the point is that it is bounded.
		hashBytesPerSecond = 500 << 20
	)
	nodeWide := connectionSlots * blockPartProofRateBytes
	assert.Less(t, nodeWide/hashBytesPerSecond, 0.05,
		"a full complement of peers sending nothing but bad proofs must not "+
			"cost more than a few percent of a core")
}

// partSetStateData returns state expecting a block it does not have, which is
// the situation in which an unfilled part index can be aimed at.
func partSetStateData(height int64) *StateData {
	stateData := &StateData{}
	stateData.Height = height
	stateData.Round = 0
	stateData.ProposalBlockParts = types.NewPartSetFromHeader(types.PartSetHeader{
		Total: 4,
		Hash:  bytes.Repeat([]byte{0x01}, crypto.HashSize),
	})
	stateData.state.ConsensusParams.Block.MaxBytes = 22020096
	return stateData
}

// invalidProofPart is a full-size block part whose proof does not check out.
// Verifying it hashes the whole leaf before the mismatch is found, which is the
// work being bounded.
func invalidProofPart(index uint32) *types.Part {
	return &types.Part{
		Index: index,
		Bytes: make([]byte, types.BlockPartSizeBytes),
		Proof: merkle.Proof{
			Total:    4,
			Index:    int64(index),
			LeafHash: bytes.Repeat([]byte{0x02}, crypto.HashSize),
			Aunts:    [][]byte{bytes.Repeat([]byte{0x03}, crypto.HashSize), bytes.Repeat([]byte{0x04}, crypto.HashSize)},
		},
	}
}
