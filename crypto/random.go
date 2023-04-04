package crypto

import (
	crand "crypto/rand"
	"encoding/hex"
	"io"
)

// CRandBytes this only uses the OS's randomness
func CRandBytes(numBytes int) []byte {
	b := make([]byte, numBytes)
	_, err := crand.Read(b)
	if err != nil {
		panic(err)
	}
	return b
}

// CRandHex returns a hex encoded string that's floor(numDigits/2) * 2 long.
//
// Note: CRandHex(24) gives 96 bits of randomness that
// are usually strong enough for most purposes.
func CRandHex(numDigits int) string {
	return hex.EncodeToString(CRandBytes(numDigits / 2))
}

// CReader returns a crand.Reader.
func CReader() io.Reader {
	return crand.Reader
}
