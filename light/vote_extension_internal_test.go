package light

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// The light client is the fourth destination the unknown-extension rejection has
// to reach: verifyBlockSignatureWithDashCore builds the canonical vote from a
// commit supplied by a provider, which is an untrusted peer. proto3 enums are
// open, so a malicious provider can put any varint in the extension type, and a
// panic here would take down the light client instead of failing the provider.
func TestClient_VerifyBlockSignatureWithDashCore_UnknownExtensionType_ReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	valSet, _ := types.RandValidatorSet(4)

	lightBlock := &types.LightBlock{
		SignedHeader: &types.SignedHeader{
			Header: &types.Header{Height: 100},
			Commit: &types.Commit{
				Height:                  100,
				Round:                   0,
				QuorumHash:              valSet.QuorumHash,
				ThresholdBlockSignature: make([]byte, types.SignatureSize),
				ThresholdVoteExtensions: []*tmproto.VoteExtension{{
					Type:      tmproto.VoteExtensionType(42),
					Extension: crypto.Checksum([]byte("x")),
					Signature: make([]byte, types.SignatureSize),
				}},
			},
		},
		ValidatorSet: valSet,
	}

	// dashCoreRPCClient is deliberately left nil: the rejection must happen while
	// building the canonical vote, before any Dash Core round-trip. A nil-pointer
	// panic here would itself be the regression.
	c := &Client{chainID: "test-chain"}

	var err error
	require.NotPanics(t, func() {
		err = c.verifyBlockSignatureWithDashCore(ctx, lightBlock)
	}, "an attacker-supplied vote-extension type must not panic the light client")
	require.ErrorIs(t, err, types.ErrUnknownVoteExtensionType,
		"the light client must reject the commit for the unknown extension type specifically")
}
