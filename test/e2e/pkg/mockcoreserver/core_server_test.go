package mockcoreserver

import (
	"context"
	"testing"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/assert"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	"github.com/dashpay/tenderdash/privval"
)

// TestMockCoreServerQuorumInfoReportsLLMQName checks that the mock reports the
// quorum type the way Dash Core does, by LLMQ name. State sync authenticates
// the light block validator set against `quorum info` and parses the type with
// btcjson.GetLLMQType; a numeric type makes every e2e state sync reject every
// snapshot and fall back to block sync.
func TestMockCoreServerQuorumInfoReportsLLMQName(t *testing.T) {
	ctx := context.Background()
	quorumHash := crypto.RandQuorumHash()
	privKey := bls12381.GenPrivKey()
	filePV := privval.GenFilePV("", "")
	filePV.UpdatePrivateKey(ctx, privKey, quorumHash, privKey.PubKey(), 1000)

	cs := &MockCoreServer{LLMQType: btcjson.LLMQType_TEST, FilePV: filePV}
	hash := quorumHash.String()
	info := cs.QuorumInfo(ctx, btcjson.QuorumCmd{QuorumHash: &hash})

	assert.Equal(t, btcjson.LLMQType_TEST.Name(), info.Type)
	assert.Equal(t, btcjson.LLMQType_TEST, btcjson.GetLLMQType(info.Type))
}
