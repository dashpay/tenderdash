package dash

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/bls12381"
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

	hostAChallenge := NewValidatorChallenge(hostANodeID, hostBNodeID, hostAProTXHash, hostBProTXHash)

	// host A: sign and send challenge
	assert.NoError(t, hostAChallenge.Sign(hostAPrivkey))

	// host B: validate and verify challenge, sign and send response
	assert.NoError(t, hostAChallenge.Validate(hostANodeID, hostBNodeID, hostAProTXHash, hostBProTXHash, nil))
	assert.NoError(t, hostAChallenge.Verify(hostAPubkey))
	response, err := hostAChallenge.Response(hostBPrivkey)
	assert.NoError(t, err)

	// host A: verify response
	assert.NoError(t, response.Verify(hostAChallenge, hostBPubkey))
}
