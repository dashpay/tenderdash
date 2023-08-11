package ed25519_test

import (
	stded25519 "crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"testing"

	voided25519 "github.com/oasisprotocol/curve25519-voi/primitives/ed25519"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/ed25519"
	tmrand "github.com/dashpay/tenderdash/libs/rand"
)

func TestSignAndValidateEd25519(t *testing.T) {

	privKey := ed25519.GenPrivKey()
	pubKey := privKey.PubKey()

	msg := crypto.CRandBytes(128)
	sig, err := privKey.SignDigest(msg)
	require.NoError(t, err)

	// Test the signature
	assert.True(t, pubKey.VerifySignature(msg, sig))

	// Mutate the signature, just one bit.
	// TODO: Replace this with a much better fuzzer, tendermint/ed25519/issues/10
	sig[7] ^= byte(0x01)

	assert.False(t, pubKey.VerifySignature(msg, sig))
}

func TestBatchSafe(t *testing.T) {
	v := ed25519.NewBatchVerifier()

	for i := 0; i <= 38; i++ {
		priv := ed25519.GenPrivKey()
		pub := priv.PubKey()

		var msg []byte
		if i%2 == 0 {
			msg = []byte("easter")
		} else {
			msg = []byte("egg")
		}

		sig, err := priv.Sign(msg)
		require.NoError(t, err)

		err = v.Add(pub, msg, sig)
		require.NoError(t, err)
	}

	ok, _ := v.Verify()
	require.True(t, ok)
}

func TestFromDer(t *testing.T) {
	var testCases = []struct {
		privkeyBase64Der        string
		expectedPubkeyBase64Der string
	}{
		{
			privkeyBase64Der:        "MC4CAQAwBQYDK2VwBCIEIB/3MZ9V0e8JidiOiDtN3Nk3sGnwohSgaAmIFuScDfOy",
			expectedPubkeyBase64Der: "MCowBQYDK2VwAyEAcpYVXaxQmDGUnlpgTe71OKv4cUcbw8k+/IeW8cZF4W4=",
		},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			privkeyDer, err := base64.StdEncoding.DecodeString(tc.privkeyBase64Der)
			require.NoError(t, err)
			expectedPubkeyDer, err := base64.StdEncoding.DecodeString(tc.expectedPubkeyBase64Der)
			require.NoError(t, err)

			expectedPubkeyStd, err := x509.ParsePKIXPublicKey(expectedPubkeyDer)
			require.NoError(t, err)
			expectedPubkey := []byte(expectedPubkeyStd.(stded25519.PublicKey))

			privkey, err := ed25519.FromDER(privkeyDer)
			assert.NoError(t, err)
			assert.Len(t, privkey, ed25519.PrivateKeySize)
			assert.Equal(t, expectedPubkey, privkey.PubKey().Bytes())
		})
	}
}

// TestEd25519StdlibCompat ensures that key format in "crypto/ed25519" is compatible with
// the keys in "github.com/oasisprotocol/curve25519-voi" (which we use).
// The intention of this test is to detect when external dependencies change the key format, breaking FromDER() logic.
func TestEd25519StdlibCompat(t *testing.T) {
	seed := tmrand.Bytes(ed25519.SeedSize)

	voiPrivkey := voided25519.NewKeyFromSeed(seed)
	voiPubkey := voiPrivkey.Public()
	stdPrivkey := stded25519.NewKeyFromSeed(seed)
	stdPubkey := stdPrivkey.Public()

	assert.EqualValues(t, voiPrivkey, stdPrivkey)
	assert.EqualValues(t, voiPrivkey.Seed(), stdPrivkey.Seed())
	assert.EqualValues(t, voiPubkey, stdPubkey)
}
