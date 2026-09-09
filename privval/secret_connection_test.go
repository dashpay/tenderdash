package privval

import (
	"errors"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	gogotypes "github.com/cosmos/gogoproto/types"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto/ed25519"
	"github.com/dashpay/tenderdash/internal/libs/protoio"
)

// lowOrderPoints lists the Curve25519 points whose order is too small to be
// safe in a Diffie-Hellman exchange. The encodings match the canonical
// libsodium blocklist.
var lowOrderPoints = [][32]byte{
	// 0 (order 4)
	{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
	// 1 (order 1)
	{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
	// order 8
	{0xe0, 0xeb, 0x7a, 0x7c, 0x3b, 0x41, 0xb8, 0xae, 0x16, 0x56, 0xe3, 0xfa, 0xf1, 0x9f, 0xc4, 0x6a,
		0xda, 0x09, 0x8d, 0xeb, 0x9c, 0x32, 0xb1, 0xfd, 0x86, 0x62, 0x05, 0x16, 0x5f, 0x49, 0xb8, 0x00},
	// order 8
	{0x5f, 0x9c, 0x95, 0xbc, 0xa3, 0x50, 0x8c, 0x24, 0xb1, 0xd0, 0xb1, 0x55, 0x9c, 0x83, 0xef, 0x5b,
		0x04, 0x44, 0x5c, 0xc4, 0x58, 0x1c, 0x8e, 0x86, 0xd8, 0x22, 0x4e, 0xdd, 0xd0, 0x9f, 0x11, 0x57},
	// p-1 (order 2)
	{0xec, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f},
	// p (order 4)
	{0xed, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f},
	// p+1 (order 1)
	{0xee, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f},
	// RFC 7748 §5 mandates that X25519 ignores the most-significant bit of
	// byte 31, so the p-1, p and p+1 encodings above each have a second valid
	// wire form with bit 7 of byte 31 set (0xff instead of 0x7f). These decode
	// to the same low-order points and must be rejected just the same.
	// p-1 (order 2), high bit set
	{0xec, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	// p (order 4), high bit set
	{0xed, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	// p+1 (order 1), high bit set
	{0xee, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
}

// TestMakeSecretConnectionRejectsLowOrderKey drives MakeSecretConnection over a
// net.Pipe against a scripted peer that sends a low-order ephemeral key. The
// scripted peer concurrently reads the local key frame so the in-tandem
// exchange in shareEphPubKey does not deadlock.
func TestMakeSecretConnectionRejectsLowOrderKey(t *testing.T) {
	for i := range lowOrderPoints {
		point := lowOrderPoints[i]
		t.Run("point-"+strconv.Itoa(i), func(t *testing.T) {
			local, remote := net.Pipe()
			defer local.Close()
			defer remote.Close()

			require.NoError(t, local.SetDeadline(time.Now().Add(5*time.Second)))
			require.NoError(t, remote.SetDeadline(time.Now().Add(5*time.Second)))

			scriptDone := make(chan error, 1)
			go func() {
				scriptDone <- scriptEphPeer(remote, point)
			}()

			_, err := MakeSecretConnection(local, ed25519.GenPrivKey())
			require.Error(t, err)

			select {
			case err := <-scriptDone:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("scripted peer did not finish")
			}
		})
	}
}

// TestMakeSecretConnectionHandshakeSucceeds confirms a normal two-ended
// handshake completes with valid ephemeral keys, so the rejection test fails
// for the right reason rather than from broken setup.
func TestMakeSecretConnectionHandshakeSucceeds(t *testing.T) {
	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()

	require.NoError(t, local.SetDeadline(time.Now().Add(5*time.Second)))
	require.NoError(t, remote.SetDeadline(time.Now().Add(5*time.Second)))

	type result struct {
		conn *SecretConnection
		err  error
	}
	remoteDone := make(chan result, 1)
	go func() {
		conn, err := MakeSecretConnection(remote, ed25519.GenPrivKey())
		remoteDone <- result{conn, err}
	}()

	localConn, err := MakeSecretConnection(local, ed25519.GenPrivKey())
	require.NoError(t, err)
	require.NotNil(t, localConn)

	res := <-remoteDone
	require.NoError(t, res.err)
	require.NotNil(t, res.conn)
}

// The ephemeral-key exchange happens before any authentication exists, and
// protoio allocates the declared length eagerly, so the read cap is the only bound
// on what an unauthenticated peer can make us allocate. The legitimate message
// encodes a 32-byte key in a 34-byte body.
func TestShareEphPubKey_RejectsOversizedMessage(t *testing.T) {
	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()

	go func() {
		// Consume the ephemeral key our side sends, then reply with a message that
		// is far larger than any legitimate one but well under the old 1 MiB cap.
		_, _ = protoio.NewDelimitedReader(remote, 1024).ReadMsg(&gogotypes.BytesValue{})
		_, _ = protoio.NewDelimitedWriter(remote).WriteMsg(
			&gogotypes.BytesValue{Value: make([]byte, maxEphemeralKeyMsgSize*2)})
	}()

	var locEphPub [32]byte
	_, err := shareEphPubKey(local, &locEphPub)
	require.ErrorIs(t, err, protoio.ErrMsgExceedsMaxSize,
		"an oversized ephemeral key message must be rejected before it is allocated")
}

// scriptEphPeer plays the remote side of shareEphPubKey: it concurrently reads
// the local ephemeral key frame and writes the supplied ephemeral key. The
// concurrent read is required because MakeSecretConnection writes its key and
// reads the peer's key in tandem over a synchronous net.Pipe.
func scriptEphPeer(conn io.ReadWriter, ephPub [32]byte) error {
	readErr := make(chan error, 1)
	go func() {
		var bz gogotypes.BytesValue
		_, err := protoio.NewDelimitedReader(conn, 1024*1024).ReadMsg(&bz)
		readErr <- err
	}()

	value := ephPub
	if _, err := protoio.NewDelimitedWriter(conn).WriteMsg(&gogotypes.BytesValue{Value: value[:]}); err != nil {
		return err
	}

	if err := <-readErr; err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
