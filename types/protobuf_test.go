package types

import (
	"testing"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	"github.com/dashpay/tenderdash/crypto/encoding"
)

func TestABCIPubKey(t *testing.T) {
	pkBLS := bls12381.GenPrivKey().PubKey()
	err := testABCIPubKey(t, pkBLS)
	assert.NoError(t, err)
}

func testABCIPubKey(t *testing.T, pk crypto.PubKey) error {
	abciPubKey, err := encoding.PubKeyToProto(pk)
	require.NoError(t, err)
	pk2, err := encoding.PubKeyFromProto(abciPubKey)
	require.NoError(t, err)
	require.Equal(t, pk, pk2)
	return nil
}

func TestABCIValidators(t *testing.T) {
	pkBLS := bls12381.GenPrivKey().PubKey()
	proTxHash := crypto.RandProTxHash()
	quorumHash := crypto.RandQuorumHash()

	// correct validator
	tmValExpected := NewValidatorDefaultVotingPower(pkBLS, proTxHash)

	tmVal := NewValidatorDefaultVotingPower(pkBLS, proTxHash)

	abciVal := TM2PB.ValidatorUpdate(tmVal)
	tmVals, err := PB2TM.ValidatorUpdates([]abci.ValidatorUpdate{abciVal})
	assert.NoError(t, err)
	assert.Equal(t, tmValExpected, tmVals[0])

	abciVals := TM2PB.ValidatorUpdates(NewValidatorSet(tmVals, tmVal.PubKey, btcjson.LLMQType_5_60, quorumHash, true))
	assert.Equal(t, abci.ValidatorSetUpdate{
		ValidatorUpdates:   []abci.ValidatorUpdate{abciVal},
		ThresholdPublicKey: *abciVal.PubKey,
		QuorumHash:         quorumHash,
	}, abciVals)

	abciVal = TM2PB.ValidatorUpdate(tmVal)
	tmVals, err = PB2TM.ValidatorUpdates([]abci.ValidatorUpdate{abciVal})
	assert.NoError(t, err)
	assert.Equal(t, tmValExpected, tmVals[0])
}

type pubKeyBLS struct{}

func (pubKeyBLS) Address() Address                              { return []byte{} }
func (pubKeyBLS) Bytes() []byte                                 { return []byte{} }
func (pubKeyBLS) VerifySignature(_ []byte, _ []byte) bool       { return false }
func (pubKeyBLS) VerifySignatureDigest(_ []byte, _ []byte) bool { return false }
func (pubKeyBLS) AggregateSignatures(_sigSharesData [][]byte, _messages [][]byte) ([]byte, error) {
	return []byte{}, nil
}
func (pubKeyBLS) VerifyAggregateSignature(_ [][]byte, _ []byte) bool { return false }
func (pubKeyBLS) Equals(crypto.PubKey) bool                          { return false }
func (pubKeyBLS) String() string                                     { return "" }
func (pubKeyBLS) HexString() string                                  { return "" }
func (pubKeyBLS) Type() string                                       { return bls12381.KeyType }
func (pubKeyBLS) TypeValue() crypto.KeyType                          { return crypto.BLS12381 }
func (pubKeyBLS) TypeTag() string                                    { return bls12381.PubKeyName }

func TestABCIValidatorFromPubKeyAndPower(t *testing.T) {
	pubkey := bls12381.GenPrivKey().PubKey()
	address := RandValidatorAddress()
	abciVal := TM2PB.NewValidatorUpdate(pubkey, DefaultDashVotingPower, crypto.RandProTxHash(), address.String())
	assert.Equal(t, DefaultDashVotingPower, abciVal.Power)
	assert.Equal(t, address.String(), abciVal.NodeAddress)

	assert.NotPanics(t, func() {
		TM2PB.NewValidatorUpdate(nil, DefaultDashVotingPower, crypto.RandProTxHash(), RandValidatorAddress().String())
	})
	assert.Panics(t, func() {
		TM2PB.NewValidatorUpdate(
			pubKeyBLS{},
			DefaultDashVotingPower,
			crypto.RandProTxHash(),
			RandValidatorAddress().String(),
		)
	})
}

func TestABCIValidatorWithoutPubKey(t *testing.T) {
	pkBLS := bls12381.GenPrivKey().PubKey()
	proTxHash := crypto.RandProTxHash()

	abciVal := TM2PB.Validator(NewValidatorDefaultVotingPower(pkBLS, proTxHash))

	// pubkey must be nil
	tmValExpected := abci.Validator{
		Power:     DefaultDashVotingPower,
		ProTxHash: proTxHash,
	}

	assert.Equal(t, tmValExpected, abciVal)
}
