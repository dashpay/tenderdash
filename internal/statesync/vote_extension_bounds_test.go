package statesync

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

func thresholdRecoverExtension(payload string) *tmproto.VoteExtension {
	return &tmproto.VoteExtension{
		Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
		Extension: []byte(payload),
		Signature: []byte("signature"),
	}
}

func withSignRequestID(extension *tmproto.VoteExtension, requestID string) *tmproto.VoteExtension {
	clone := *extension
	clone.XSignRequestId = &tmproto.VoteExtension_SignRequestId{
		SignRequestId: []byte(requestID),
	}
	return &clone
}

// A THRESHOLD_RECOVER sign hash is built from the extension, type, height, round
// and chain ID; the request ID is dropped during canonicalization. Repeating one
// genuine extension under distinct request IDs therefore leaves every copy
// verifying against the signature it was cloned from, so the duplicate check has
// to ignore the request ID for this type or the bound buys nothing.
func TestValidateThresholdVoteExtensions_RepeatsUnderDistinctSignRequestIDs(t *testing.T) {
	genuine := thresholdRecoverExtension("payload")

	extensions := tmproto.VoteExtensions{genuine}
	for i := 1; i < maxThresholdVoteExtensions; i++ {
		extensions = append(extensions, withSignRequestID(genuine, fmt.Sprintf("dpevote%03d", i)))
	}

	err := validateThresholdVoteExtensions(extensions)
	require.Error(t, err, "repeating one extension under distinct request IDs must be rejected")
	require.Contains(t, err.Error(), "duplicate threshold vote extension")
}

// The request ID is part of the sign hash for THRESHOLD_RECOVER_RAW, so entries
// that differ only there are genuinely distinct and must still be accepted.
func TestValidateThresholdVoteExtensions_RawKeepsDistinctSignRequestIDs(t *testing.T) {
	raw := &tmproto.VoteExtension{
		Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW,
		Extension: []byte("payload"),
		Signature: []byte("signature"),
	}

	extensions := tmproto.VoteExtensions{
		withSignRequestID(raw, "dpevote001"),
		withSignRequestID(raw, "dpevote002"),
	}

	require.NoError(t, validateThresholdVoteExtensions(extensions))
}

func TestValidateThresholdVoteExtensions_RejectsIdenticalEntries(t *testing.T) {
	genuine := thresholdRecoverExtension("payload")

	err := validateThresholdVoteExtensions(tmproto.VoteExtensions{genuine, genuine})
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate threshold vote extension")
}

func TestValidateThresholdVoteExtensions_AcceptsDistinctPayloads(t *testing.T) {
	extensions := tmproto.VoteExtensions{
		thresholdRecoverExtension("first"),
		thresholdRecoverExtension("second"),
	}

	require.NoError(t, validateThresholdVoteExtensions(extensions))
}

func TestValidateThresholdVoteExtensions_RejectsOverBound(t *testing.T) {
	extensions := make(tmproto.VoteExtensions, 0, maxThresholdVoteExtensions+1)
	for i := 0; i <= maxThresholdVoteExtensions; i++ {
		extensions = append(extensions, thresholdRecoverExtension(fmt.Sprintf("payload%d", i)))
	}

	err := validateThresholdVoteExtensions(extensions)
	require.Error(t, err)
	require.Contains(t, err.Error(), "too many threshold vote extensions")
}
