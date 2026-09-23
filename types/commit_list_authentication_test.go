package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

func TestCommitListMustBeAuthenticated(t *testing.T) {
	ctx := context.Background()
	vs, vals, keys := randVoteSet(ctx, t, 3, 0, tmproto.PrecommitType, 4)
	id := makeBlockIDRandom()
	commit, err := makeCommit(ctx, id, 3, 0, vs, keys)
	require.NoError(t, err)
	require.NoError(t, vals.VerifyCommit(vs.ChainID(), id, 3, commit))
	for _, mode := range []string{"strip", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			wire := commit.ToProto()
			if mode == "strip" {
				wire.ThresholdVoteExtensions = nil
			} else {
				wire.ThresholdVoteExtensions = append(wire.ThresholdVoteExtensions, wire.ThresholdVoteExtensions[0])
			}
			changed, err := CommitFromProto(wire)
			require.NoError(t, err)
			require.Equal(t, commit.Hash(), changed.Hash())
			require.Error(t, vals.VerifyCommit(vs.ChainID(), id, 3, changed), "modified list must be rejected")
		})
	}
}

// This intentionally failing probe exposes missing context binding, not a requirement
// to change Core-compatible RAW signing; replay protection may belong in application validation.
func TestRawVoteExtensionReplayMustBeRejected(t *testing.T) {
	ext, err := VoteExtensionFromProto(tmproto.VoteExtension{
		Type:           tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW,
		Extension:      crypto.Checksum([]byte("withdrawal")),
		XSignRequestId: &tmproto.VoteExtension_SignRequestId{SignRequestId: []byte("withdrawal-request")},
	})
	require.NoError(t, err)
	q := crypto.Checksum([]byte("quorum"))
	a, err := ext.SignItem("chain", 3, 0, 106, q)
	require.NoError(t, err)
	b, err := ext.SignItem("chain", 100, 4, 106, q)
	require.NoError(t, err)
	require.NotEqual(t, a.SignHash, b.SignHash, "different height and round must prevent replay")
}
