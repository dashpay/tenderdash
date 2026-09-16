package mockcoreserver

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	"github.com/dashpay/tenderdash/crypto/encoding"
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
	// Dash Core reports keys as plain hex, not as the PubKey String() form.
	thresholdKey, err := hex.DecodeString(info.QuorumPublicKey)
	require.NoError(t, err)
	assert.Equal(t, privKey.PubKey().Bytes(), thresholdKey)
}

// TestMockCoreServerServesCompleteQuorums checks that a quorum built from the
// validator set schedule is answered in full - every member with its public
// key share - whatever the case of the requested quorum hash.
func TestMockCoreServerServesCompleteQuorums(t *testing.T) {
	ctx := context.Background()
	quorumHash := crypto.RandQuorumHash()
	thresholdKey := bls12381.GenPrivKey().PubKey()
	validators := make([]abci.ValidatorUpdate, 0, 3)
	for range 3 {
		pubKey := encoding.MustPubKeyToProto(bls12381.GenPrivKey().PubKey())
		validators = append(validators, abci.ValidatorUpdate{
			PubKey:    &pubKey,
			Power:     100,
			ProTxHash: crypto.RandProTxHash(),
		})
	}
	quorums, err := QuorumsFromValidatorSetUpdates(btcjson.LLMQType_TEST, map[int64]abci.ValidatorSetUpdate{
		1000: {
			ValidatorUpdates:   validators,
			ThresholdPublicKey: encoding.MustPubKeyToProto(thresholdKey),
			QuorumHash:         quorumHash,
		},
	})
	require.NoError(t, err)

	cs := &MockCoreServer{LLMQType: btcjson.LLMQType_TEST, FilePV: privval.GenFilePV("", ""), Quorums: quorums}
	hash := strings.ToUpper(hex.EncodeToString(quorumHash))
	info := cs.QuorumInfo(ctx, btcjson.QuorumCmd{QuorumHash: &hash})

	assert.Equal(t, btcjson.LLMQType_TEST, btcjson.GetLLMQType(info.Type))
	assert.Equal(t, hex.EncodeToString(thresholdKey.Bytes()), info.QuorumPublicKey)
	require.Len(t, info.Members, len(validators))
	for i, validator := range validators {
		member := info.Members[i]
		assert.Equal(t, hex.EncodeToString(validator.ProTxHash), member.ProTxHash)
		assert.Equal(t, hex.EncodeToString(validator.PubKey.GetBls12381()), member.PubKeyShare)
		assert.True(t, member.Valid)
	}
}
