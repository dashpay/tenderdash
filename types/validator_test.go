package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tendermint/tendermint/crypto"
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
