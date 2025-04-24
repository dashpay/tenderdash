package types_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dashpay/tenderdash/proto/tendermint/types"
)

func TestMarshalVoteExtension(t *testing.T) {
	testCases := []struct {
		extension types.VoteExtension
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

		{
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

			marshaled, err := v.Marshal()
			assert.NoError(t, err)
			assert.NotEmpty(t, marshaled)
		})
	}

}
