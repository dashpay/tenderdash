package types_test

import (
	"testing"

	"github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/stretchr/testify/assert"
)

func TestMarshalVoteExtension(t *testing.T) {
	testCases := []struct {
		extension   types.VoteExtension
		expectPanic bool
	}{
		{
			extension: types.VoteExtension{
				Type:      types.VoteExtensionType_THRESHOLD_RECOVER_RAW,
				Extension: []byte("threshold"),
				XSignRequestId: &types.VoteExtension_SignRequestId{
					SignRequestId: []byte("sign-request-id"),
				}}},
		{
			extension: types.VoteExtension{
				Type:           types.VoteExtensionType_THRESHOLD_RECOVER_RAW,
				Extension:      []byte("threshold"),
				XSignRequestId: nil,
			}},
		{
			extension: types.VoteExtension{
				Type:           types.VoteExtensionType_THRESHOLD_RECOVER_RAW,
				Extension:      []byte("threshold"),
				XSignRequestId: &types.VoteExtension_SignRequestId{nil},
			}},
		// Test below panics because of nil pointer dereference bug in gogoproto
		// FIXME: remove expectPanic when we replace gogoproto
		{
			expectPanic: true,
			extension: types.VoteExtension{
				Type:           types.VoteExtensionType_THRESHOLD_RECOVER_RAW,
				Extension:      []byte("threshold"),
				XSignRequestId: (*types.VoteExtension_SignRequestId)(nil),
			}},
		{
			extension: (&types.VoteExtension{
				Type:           types.VoteExtensionType_THRESHOLD_RECOVER_RAW,
				Extension:      []byte("threshold"),
				XSignRequestId: (*types.VoteExtension_SignRequestId)(nil),
			}).Clone()},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			// marshaled, err := protoio.MarshalDelimited(tc)
			// assert.NoError(t, err)
			// assert.NotEmpty(t, marshaled)

			v := types.Vote{
				Type:           types.PrecommitType,
				VoteExtensions: []*types.VoteExtension{&tc.extension},
			}
			f := func() {
				marshaled, err := v.Marshal()
				assert.NoError(t, err)
				assert.NotEmpty(t, marshaled)
			}

			if tc.expectPanic {
				assert.Panics(t, f)
			} else {
				assert.NotPanics(t, f)
			}
		})
	}

}
