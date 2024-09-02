package privval

import (
	"testing"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/bytes"
)

func TestHardcodedQuorumInfo(t *testing.T) {
	// Test that the hardcoded quorum info is correct
	quorumHashS := "00000000000000105f2d1ceda3c63d2b677a227d7ed77c5bad3776725cad0002"
	quorumHash := bytes.MustHexDecode(quorumHashS)
	qi, err := HardcodedQuorumInfo(btcjson.LLMQType_100_67, quorumHash)
	require.NoError(t, err)
	assert.Len(t, qi.Members, 100)
	assert.NotEmpty(t, qi.Members[0].PubKeyShare)
}
