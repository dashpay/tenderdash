package types

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
)

func TestValidatorProtoBuf(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	val, _ := randValidatorInQuorum(ctx, t, crypto.RandQuorumHash())
	testCases := []struct {
		msg      string
		v1       *Validator
		expPass1 bool
		expPass2 bool
	}{
		{"success validator", val, true, true},
		{"failure empty", &Validator{}, false, false},
		{"failure nil", nil, false, false},
	}
	for _, tc := range testCases {
		protoVal, err := tc.v1.ToProto()

		if tc.expPass1 {
			require.NoError(t, err, tc.msg)
		} else {
			require.Error(t, err, tc.msg)
		}

		val, err := ValidatorFromProto(protoVal)
		if tc.expPass2 {
			require.NoError(t, err, tc.msg)
			require.Equal(t, tc.v1, val, tc.msg)
		} else {
			require.Error(t, err, tc.msg)
		}
	}
}

func TestValidatorValidateBasic(t *testing.T) {
	quorumHash := crypto.RandQuorumHash()
	priv := NewMockPVForQuorum(quorumHash)
	pubKey, err := priv.GetPubKey(context.Background(), quorumHash)
	require.NoError(t, err)

	testCases := []struct {
		val *Validator
		err bool
		msg string
	}{
		{
			val: NewValidatorDefaultVotingPower(pubKey, priv.ProTxHash),
			err: false,
			msg: "no error",
		},
		{
			val: nil,
			err: true,
			msg: "nil validator",
		},
		{
			val: &Validator{
				PubKey:      nil,
				ProTxHash:   crypto.RandProTxHash(),
				VotingPower: 100,
			},
			err: false,
			msg: "no error",
		},
		{
			val: NewValidator(pubKey, -1, priv.ProTxHash, ""),
			err: true,
			msg: "validator has negative voting power",
		},
		{
			val: &Validator{
				PubKey:    pubKey,
				ProTxHash: crypto.CRandBytes(12),
			},
			err: true,
			msg: "validator proTxHash is the wrong size: 12",
		},
		{
			val: &Validator{
				PubKey:    pubKey,
				ProTxHash: nil,
			},
			err: true,
			msg: "validator does not have a provider transaction hash",
		},
	}

	for _, tc := range testCases {
		tcRun := tc
		t.Run(tc.msg, func(t *testing.T) {
			err := tcRun.val.ValidateBasic()
			if tcRun.err {
				if assert.Error(t, err) {
					assert.Equal(t, tcRun.msg, err.Error())
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidatorSetHashVectors checks if provided validator threshold pubkey and quorum hash returns expected hash
func TestValidatorSetHashVectors(t *testing.T) {
	thresholdPublicKey, err := base64.RawStdEncoding.DecodeString("gw5F5F5kFNnWFUc8woFOaxccUI+cd+ixaSS3RZT2HJlWpvoWM16YRn6sjYvbdtGH")
	require.NoError(t, err)

	quorumHash, err := hex.DecodeString("703ee5bfc78765cc9e151d8dd84e30e196ababa83ac6cbdee31a88a46bba81b9")
	require.NoError(t, err)

	expected := "81742F95E99EAE96ABC727FE792CECB4996205DE6BFC88AFEE1F60B96BC648B2"

	valset := ValidatorSet{
		ThresholdPublicKey: bls12381.PubKey(thresholdPublicKey),
		QuorumHash:         quorumHash,
	}

	hash := valset.Hash()
	assert.Equal(t, expected, hash.String())
}
