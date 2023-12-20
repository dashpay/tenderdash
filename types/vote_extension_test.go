package types

import (
	"testing"

	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVoteExtensionCopySignsFromProto(t *testing.T) {
	src := tmproto.VoteExtensions{
		&tmproto.VoteExtension{
			Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
			Extension: []byte("threshold"),
			Signature: []byte("signature"),
		},
	}

	dst := VoteExtensions{&ThresholdVoteExtension{}}
	err := dst.CopySignsFromProto(src)
	require.NoError(t, err)
	assert.EqualValues(t, src[0].GetSignature(), dst[0].GetSignature())
}
