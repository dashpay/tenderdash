package types

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

func manyExtensionProtoVote(t testing.TB, n int) *tmproto.Vote {
	exts := make([]*tmproto.VoteExtension, n)
	for i := range exts {
		exts[i] = &tmproto.VoteExtension{
			Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
			Extension: []byte("x"),
			Signature: make([]byte, 96),
		}
	}
	return (&Vote{
		Type:           tmproto.PrecommitType,
		Height:         1,
		Round:          0,
		VoteExtensions: MustVoteExtensionsFromProto(t, exts...),
	}).ToProto()
}

// A single message must not be able to force an unbounded number of BLS
// verifications. The bound lives on the VERIFICATION path (makeVerifyQuorumSigns),
// the shared chokepoint for commit and vote verification — and deliberately NOT
// on MakeQuorumSigns, which a validator also uses to sign its OWN votes and must
// never be blocked from doing so, whatever extension count the application emits.
func TestVoteExtensionCap_SigningUncapped_VerificationCapped(t *testing.T) {
	quorumHash := crypto.Checksum([]byte("q"))

	// Signing path: never rejected for extension count.
	_, err := MakeQuorumSigns("test-chain", 106, quorumHash, manyExtensionProtoVote(t, MaxVoteExtensions+1))
	require.NoError(t, err, "signing path must not be bounded by MaxVoteExtensions")

	// Verification path, over the cap: rejected by the count check, before any
	// per-extension work.
	_, err = makeVerifyQuorumSigns("test-chain", 106, quorumHash, manyExtensionProtoVote(t, MaxVoteExtensions+1))
	require.Error(t, err)
	require.Contains(t, err.Error(), "too many vote extensions")

	// Verification path, at the cap: not rejected for count (Platform's real max
	// is 4; the cap keeps headroom, so legitimate traffic is never rejected here).
	_, err = makeVerifyQuorumSigns("test-chain", 106, quorumHash, manyExtensionProtoVote(t, MaxVoteExtensions))
	if err != nil {
		require.NotContains(t, err.Error(), "too many vote extensions",
			"a message at the cap must not be rejected for extension count")
	}
}

// The commit verification path enforces the same bound, so an over-cap Commit
// is rejected without performing one BLS verification per extension.
func TestVerifyCommit_TooManyThresholdExtensions_Rejected(t *testing.T) {
	vals, _ := RandValidatorSet(4)

	exts := make([]*tmproto.VoteExtension, MaxVoteExtensions+1)
	for i := range exts {
		exts[i] = &tmproto.VoteExtension{
			Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
			Extension: []byte("x"),
			Signature: make([]byte, 96),
		}
	}
	commit := &Commit{
		Height:                  100,
		Round:                   0,
		BlockID:                 BlockID{Hash: crypto.Checksum([]byte("b")), PartSetHeader: PartSetHeader{Total: 1, Hash: crypto.Checksum([]byte("p"))}, StateID: crypto.Checksum([]byte("s"))},
		QuorumHash:              vals.QuorumHash,
		ThresholdBlockSignature: make([]byte, SignatureSize),
		ThresholdVoteExtensions: exts,
	}

	var err error
	require.NotPanics(t, func() {
		err = vals.VerifyCommit("test-chain", commit.BlockID, commit.Height, commit)
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "too many vote extensions")
}
