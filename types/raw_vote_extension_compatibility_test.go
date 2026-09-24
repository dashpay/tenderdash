package types

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

// Core-compatible RAW signatures intentionally omit the Tenderdash height and round.
func TestRawVoteExtensionPreservesCoreSigningAcrossHeights(t *testing.T) {
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
	require.Equal(t, a.SignHash, b.SignHash, "Core-compatible RAW signing must remain independent of Tenderdash height and round")
}
