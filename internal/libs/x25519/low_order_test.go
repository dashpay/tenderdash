package x25519_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/curve25519"

	"github.com/dashpay/tenderdash/internal/libs/x25519"
)

// TestX25519RejectsLowOrderPoints pins the standard-library behavior the
// secret-connection handshakes rely on: curve25519.X25519 (backed by
// crypto/ecdh) returns an error for a low-order point rather than producing an
// all-zero shared secret. If a future toolchain regressed this, the handshakes
// would silently accept such remote ephemeral keys, so the guard lives here.
func TestX25519RejectsLowOrderPoints(t *testing.T) {
	// A fixed, nonzero 32-byte scalar is sufficient; the result depends on the
	// point being low-order, not on the scalar value.
	var scalar [32]byte
	for i := range scalar {
		scalar[i] = byte(i + 1)
	}

	for i := range x25519.LowOrderPoints {
		point := x25519.LowOrderPoints[i]
		_, err := curve25519.X25519(scalar[:], point[:])
		require.Error(t, err, "X25519 must reject low-order point %d", i)
	}
}
