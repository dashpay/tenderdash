package types_test

import (
	"testing"

	"github.com/dashpay/tenderdash/internal/libs/protoio"
	"github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/stretchr/testify/assert"
)

func TestMarshalVoteExtension(t *testing.T) {

	ve := &types.VoteExtension{
		Type:      types.VoteExtensionType_THRESHOLD_RECOVER_RAW,
		Extension: []byte("threshold"),
		XSignRequestId: &types.VoteExtension_SignRequestId{
			SignRequestId: []byte("sign-request-id"),
		},
	}

	v := types.Vote{
		Type:           types.PrecommitType,
		VoteExtensions: []*types.VoteExtension{ve},
	}
	v.Marshal()

	ve.Marshal()
	marshaled, err := protoio.MarshalDelimited(ve)
	assert.NoError(t, err)
	assert.NotEmpty(t, marshaled)
}
