package dash

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/bls12381"
	"github.com/tendermint/tendermint/libs/rand"
	"github.com/tendermint/tendermint/types"
)

// TestControlChallengeResponse executes challenge-response protocol between hosts A and B
func TestControlChallengeResponse(t *testing.T) {
	hostAPrivkey := bls12381.GenPrivKey()
	hostAPubkey := hostAPrivkey.PubKey()
	hostAProTXHash := crypto.RandProTxHash()
	hostANodeID := types.RandValidatorAddress().NodeID

	hostBPrivkey := bls12381.GenPrivKey()
	hostBPubkey := hostBPrivkey.PubKey()
	hostBProTXHash := crypto.RandProTxHash()
	hostBNodeID := types.RandValidatorAddress().NodeID

	quorumHash := crypto.RandQuorumHash()

	hostAChallenge := NewValidatorChallenge(hostANodeID, hostBNodeID, hostAProTXHash, hostBProTXHash, quorumHash)

	// host A: sign and send challenge
	assert.NoError(t, hostAChallenge.Sign(hostAPrivkey))

	// host B: validate and verify challenge, sign and send response
	assert.NoError(t, hostAChallenge.Validate(hostANodeID, hostBNodeID, hostAProTXHash, hostBProTXHash))
	assert.NoError(t, hostAChallenge.Verify(hostAPubkey))
	response, err := NewResponse(hostAChallenge, hostBPrivkey)
	assert.NoError(t, err)

	// host A: verify response
	assert.NoError(t, response.Verify(hostAChallenge, hostBPubkey))
}

func TestControlValidate(t *testing.T) {
	senderID := string(types.RandValidatorAddress().NodeID)
	recipientID := string(types.RandValidatorAddress().NodeID)
	senderProTxHash := crypto.RandProTxHash()
	recipientProTxHash := crypto.RandProTxHash()
	quorumHash := crypto.RandQuorumHash()
	token := rand.Bytes(8)

	testCases := []struct {
		Description        string
		Sum                isControlMessage_Sum
		validateShouldFail bool
	}{
		{
			Description: "ValidatorChallenge valid",
			Sum: &ControlMessage_ValidatorChallenge{
				ValidatorChallenge: &ValidatorChallenge{
					SenderProTxHash:    senderProTxHash,
					RecipientProTxHash: recipientProTxHash,
					SenderNodeID:       senderID,
					RecipientNodeID:    recipientID,
					QuorumHash:         quorumHash,
					Token:              token,
					Signature:          nil,
				},
			},
		},
		{
			Description: "ValidatorChallenge+sig valid",
			Sum: &ControlMessage_ValidatorChallenge{
				ValidatorChallenge: &ValidatorChallenge{
					SenderProTxHash:    senderProTxHash,
					RecipientProTxHash: recipientProTxHash,
					SenderNodeID:       senderID,
					RecipientNodeID:    recipientID,
					QuorumHash:         quorumHash,
					Token:              token,
					Signature:          rand.Bytes(bls12381.SignatureSize),
				},
			},
		},
		{
			Description: "ValidatorChallenge+sig wrong recipient hash",
			Sum: &ControlMessage_ValidatorChallenge{
				ValidatorChallenge: &ValidatorChallenge{
					SenderProTxHash:    senderProTxHash,
					RecipientProTxHash: crypto.RandProTxHash(),
					SenderNodeID:       senderID,
					RecipientNodeID:    recipientID,
					QuorumHash:         quorumHash,
					Token:              []byte{0x1, 0x2, 0x3, 0x4},
					Signature:          rand.Bytes(bls12381.SignatureSize),
				},
			},
			validateShouldFail: true,
		},
		{
			Description: "ValidatorChallenge+sig wrong sig",
			Sum: &ControlMessage_ValidatorChallenge{
				ValidatorChallenge: &ValidatorChallenge{
					SenderProTxHash:    senderProTxHash,
					RecipientProTxHash: recipientProTxHash,
					SenderNodeID:       senderID,
					RecipientNodeID:    recipientID,
					QuorumHash:         quorumHash,
					Token:              token,
					Signature:          rand.Bytes(bls12381.SignatureSize - 1),
				},
			},
			validateShouldFail: true,
		},
		{
			Description: "ValidatorChallengeResponse no sig",
			Sum: &ControlMessage_ValidatorChallengeResponse{
				ValidatorChallengeResponse: &ValidatorChallengeResponse{
					Signature: nil,
				},
			},
			validateShouldFail: true,
		},
		{
			Description: "ValidatorChallengeResponse wrong sig",
			Sum: &ControlMessage_ValidatorChallengeResponse{
				ValidatorChallengeResponse: &ValidatorChallengeResponse{
					Signature: rand.Bytes(bls12381.SignatureSize - 1),
				},
			},
			validateShouldFail: true,
		},
		{
			Description: "ValidatorChallengeResponse ok",
			Sum: &ControlMessage_ValidatorChallengeResponse{
				ValidatorChallengeResponse: &ValidatorChallengeResponse{
					Signature: rand.Bytes(bls12381.SignatureSize),
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Description, func(t *testing.T) {
			ctrl := &ControlMessage{Sum: tc.Sum}
			msg, err := ctrl.Unwrap()
			require.NoError(t, err, "Unwrap")

			ctrl = &ControlMessage{}
			err = ctrl.Wrap(msg)
			require.NoError(t, err, "Wrap")

			err = ctrl.Validate(types.NodeID(senderID), types.NodeID(recipientID), senderProTxHash, recipientProTxHash)
			if tc.validateShouldFail {
				require.Error(t, err, "Validate")
				return
			}
			require.NoError(t, err, "Validate")
		})
	}
}

func TestControlWrapFail(t *testing.T) {
	ctrl := &ControlMessage{}
	msg := &ControlMessage{}
	require.Error(t, ctrl.Wrap(msg))
}
