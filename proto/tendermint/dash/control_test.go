package dash

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tendermint/tendermint/crypto/bls12381"
	"github.com/tendermint/tendermint/libs/rand"
)

// TestControlChallengeResponse executes challenge-response protocol between hosts A and B
func TestControlChallengeResponse(t *testing.T) {
	hostAPrivkey := bls12381.GenPrivKey()
	hostAPubkey := hostAPrivkey.PubKey()
	hostAChallenge := rand.Bytes(ChallengeSize)

	hostBPrivkey := bls12381.GenPrivKey()
	hostBPubkey := hostBPrivkey.PubKey()

	challenge := ValidatorChallenge{
		Challenge: hostAChallenge,
	}

	// host A: sign and send challenge
	assert.NoError(t, challenge.Sign(hostAPrivkey))

	// host B: verify challenge, sign and send response
	assert.NoError(t, challenge.Verify(hostAPubkey))
	response, err := challenge.Response(hostBPrivkey)
	assert.NoError(t, err)

	// host A: verify response
	assert.NoError(t, response.Verify(hostAChallenge, hostBPubkey))
}
