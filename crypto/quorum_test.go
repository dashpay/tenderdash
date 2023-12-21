package crypto_test

import (
	"encoding/hex"
	"testing"

	"github.com/dashpay/tenderdash/crypto"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/stretchr/testify/assert"
)

func TestQuorumSignItem(t *testing.T) {

	si := crypto.SignItem{
		ID:         mustHexDecode("87cda9461081793e7e31ab1def8ffbd453775a0f9987304598398d42a78d68d4"),
		RawHash:    mustHexDecode("5ef9b9eecc4df7c5aee677c0a72816f4515999a539003cf4bbb6c15c39634c31"),
		LlmqType:   106,
		QuorumHash: mustHexDecode("366f07c9b80a2661563a33c09f02156720159b911186b4438ff281e537674771"),
	}
	si.UpdateSignHash(true)

	expectID := tmbytes.Reverse(mustHexDecode("94635358f4c75a1d0b38314619d1c5d9a16f12961b5314d857e04f2eb61d78d2"))

	assert.EqualValues(t, expectID, si.SignHash)
}

func mustHexDecode(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}
