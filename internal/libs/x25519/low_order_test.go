package x25519_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/libs/x25519"
)

// TestIsLowOrderPoint verifies that every blocklisted Curve25519 point is
// detected in both encodings (high bit clear and set) and that ordinary keys
// are accepted.
func TestIsLowOrderPoint(t *testing.T) {
	for i, p := range x25519.LowOrderPoints {
		p := p
		require.True(t, x25519.IsLowOrderPoint(&p), "point %d must be detected", i)

		// Setting the high bit of the final byte must not bypass the check.
		highBitSet := p
		highBitSet[31] |= 0x80
		require.True(t, x25519.IsLowOrderPoint(&highBitSet), "point %d with high bit set must be detected", i)
	}

	// A normal key must not be flagged.
	var legitimate [32]byte
	for i := range legitimate {
		legitimate[i] = byte(i + 1)
	}
	require.False(t, x25519.IsLowOrderPoint(&legitimate), "an ordinary key must not be flagged")
}
