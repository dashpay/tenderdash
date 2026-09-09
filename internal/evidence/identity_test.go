package evidence

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	tmrand "github.com/dashpay/tenderdash/libs/rand"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func testBlockID(seed byte) types.BlockID {
	hash := make([]byte, crypto.HashSize)
	for i := range hash {
		hash[i] = seed
	}
	return types.BlockID{
		Hash:          hash,
		PartSetHeader: types.PartSetHeader{Total: 1, Hash: hash},
		StateID:       hash,
	}
}

func testEvidence(height int64, round int32, proTxHash crypto.ProTxHash, a, b byte) *types.DuplicateVoteEvidence {
	vote := func(blockID types.BlockID, sig byte) *types.Vote {
		signature := make([]byte, 96)
		signature[0] = sig
		return &types.Vote{
			Type:               tmproto.PrecommitType,
			Height:             height,
			Round:              round,
			BlockID:            blockID,
			ValidatorProTxHash: proTxHash,
			BlockSignature:     signature,
		}
	}
	return &types.DuplicateVoteEvidence{
		VoteA:     vote(testBlockID(a), 1),
		VoteB:     vote(testBlockID(b), 2),
		Timestamp: time.Unix(0, 0),
	}
}

// TestEvidenceIdentityIgnoresMutableBytes is the property the whole
// de-duplication rests on: an attacker may re-encode a piece of evidence any
// way it likes without changing what the evidence alleges, so the key must
// depend only on the allegation.
func TestEvidenceIdentityIgnoresMutableBytes(t *testing.T) {
	proTxHash := crypto.ProTxHash(tmrand.Bytes(crypto.ProTxHashSize))
	original := testEvidence(10, 2, proTxHash, 0x11, 0x22)

	want, ok := evidenceIdentity(original)
	require.True(t, ok)

	t.Run("flipped signature", func(t *testing.T) {
		mutated := testEvidence(10, 2, proTxHash, 0x11, 0x22)
		mutated.VoteA.BlockSignature[3] ^= 0xff
		got, ok := evidenceIdentity(mutated)
		require.True(t, ok)
		assert.Equal(t, want, got, "a flipped signature byte must not buy a new identity")
	})

	t.Run("rewritten abci fields", func(t *testing.T) {
		mutated := testEvidence(10, 2, proTxHash, 0x11, 0x22)
		mutated.TotalVotingPower = 12345
		mutated.ValidatorPower = 678
		mutated.Timestamp = time.Unix(99999, 0)
		got, ok := evidenceIdentity(mutated)
		require.True(t, ok)
		assert.Equal(t, want, got, "rewritten ABCI fields must not buy a new identity")
	})

	t.Run("swapped votes", func(t *testing.T) {
		swapped := testEvidence(10, 2, proTxHash, 0x22, 0x11)
		got, ok := evidenceIdentity(swapped)
		require.True(t, ok)
		assert.Equal(t, want, got, "which vote is carried as A must not change the identity")
	})
}

// TestEvidenceIdentityDistinguishesAllegations guards the other direction: two
// different accusations must never collide, or holding evidence of one would
// suppress the other.
func TestEvidenceIdentityDistinguishesAllegations(t *testing.T) {
	proTxHash := crypto.ProTxHash(tmrand.Bytes(crypto.ProTxHashSize))
	other := crypto.ProTxHash(tmrand.Bytes(crypto.ProTxHashSize))
	base, ok := evidenceIdentity(testEvidence(10, 2, proTxHash, 0x11, 0x22))
	require.True(t, ok)

	for name, ev := range map[string]*types.DuplicateVoteEvidence{
		"different height":    testEvidence(11, 2, proTxHash, 0x11, 0x22),
		"different round":     testEvidence(10, 3, proTxHash, 0x11, 0x22),
		"different validator": testEvidence(10, 2, other, 0x11, 0x22),
		"different block":     testEvidence(10, 2, proTxHash, 0x11, 0x33),
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := evidenceIdentity(ev)
			require.True(t, ok)
			assert.NotEqual(t, base, got)
		})
	}

	t.Run("different vote type", func(t *testing.T) {
		ev := testEvidence(10, 2, proTxHash, 0x11, 0x22)
		ev.VoteA.Type = tmproto.PrevoteType
		ev.VoteB.Type = tmproto.PrevoteType
		got, ok := evidenceIdentity(ev)
		require.True(t, ok)
		assert.NotEqual(t, base, got)
	})
}

// TestIdentitySetEvictsOldestFirst pins the bound and its ordering. The set
// must not grow without limit — an unbounded memory of everything we have seen
// is its own denial of service — and what it forgets first must be the
// evidence closest to aging out of the window anyway.
func TestIdentitySetEvictsOldestFirst(t *testing.T) {
	const capacity = 16
	set := newIdentitySet(capacity)

	proTxHash := crypto.ProTxHash(tmrand.Bytes(crypto.ProTxHashSize))
	for height := int64(1); height <= capacity*4; height++ {
		set.add(testEvidence(height, 0, proTxHash, 0x11, 0x22))
		require.LessOrEqual(t, set.size(), capacity, "the set must never exceed its capacity")
	}

	assert.True(t, set.has(testEvidence(capacity*4, 0, proTxHash, 0x11, 0x22)),
		"the newest identity must be retained")
	assert.False(t, set.has(testEvidence(1, 0, proTxHash, 0x11, 0x22)),
		"the oldest identity must be evicted first")
}
