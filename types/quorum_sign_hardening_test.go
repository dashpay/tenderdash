package types

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// A rejected vote-extension error is logged once per bad peer message, and its
// inputs are attacker-controlled. It must not echo back the raw extension bytes,
// hashes, or signature, which would turn verification into a log-amplification
// vector.
func TestVerifyVoteExtensions_ErrorOmitsAttackerBytes(t *testing.T) {
	q := QuorumSignData{VoteExtensionSignItems: []SignItem{{
		SignHash: make([]byte, 32),
		Msg:      []byte("attacker-raw-msg"),
		MsgHash:  []byte("attacker-hash"),
	}}}
	// Counts match (1 and 1) so verification runs; pubKeyBLS always fails it.
	sigs := QuorumSigns{VoteExtensionSignatures: [][]byte{[]byte("bad-sig")}}

	err := q.VerifyVoteExtensions(pubKeyBLS{}, sigs)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "raw msg")
	require.NotContains(t, err.Error(), "sigHash")
	require.Contains(t, err.Error(), "vote-extension 0 signature is invalid")
}

// Block-signature verification runs before, and short-circuits, the far more
// expensive per-extension verification. When the block signature is invalid the
// extensions must never be inspected.
func TestVerify_ShortCircuitsOnBlockSignature(t *testing.T) {
	q := QuorumSignData{
		Block:                  SignItem{SignHash: make([]byte, 32)},
		VoteExtensionSignItems: []SignItem{{SignHash: make([]byte, 32)}}, // 1 expected
	}
	// BlockSign present but invalid (pubKeyBLS fails it); zero extension
	// signatures would be a count mismatch IF the extensions were reached.
	sigs := QuorumSigns{BlockSign: []byte("bad-block-sig")}

	err := q.Verify(pubKeyBLS{}, sigs)
	require.ErrorIs(t, err, ErrVoteInvalidBlockSignature)

	var mismatch ErrVoteExtensionCountMismatch
	require.False(t, errors.As(err, &mismatch),
		"extensions must not be verified once the block signature is invalid")
}

// A vote-extension count mismatch is an application/version disagreement an
// honest peer reaches, not forgery. It must be a distinct typed error so commit
// verification can avoid evicting the sender for it.
func TestVerifyVoteExtensions_CountMismatchTyped(t *testing.T) {
	q := QuorumSignData{VoteExtensionSignItems: []SignItem{{SignHash: make([]byte, 32)}}}

	err := q.VerifyVoteExtensions(pubKeyBLS{}, QuorumSigns{}) // 1 expected, 0 provided
	require.Error(t, err)

	var mismatch ErrVoteExtensionCountMismatch
	require.True(t, errors.As(err, &mismatch),
		"a count mismatch must be a typed, non-forgery error")
}
