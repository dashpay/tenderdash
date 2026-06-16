package conn

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
	"github.com/dashpay/tenderdash/internal/libs/x25519"
)

// TestMakeSecretConnectionRejectsLowOrderKey drives MakeSecretConnection over a
// net.Pipe against a scripted peer that sends a low-order ephemeral key. The
// scripted peer concurrently reads the local key frame so the in-tandem
// exchange in shareEphPubKey does not deadlock.
func TestMakeSecretConnectionRejectsLowOrderKey(t *testing.T) {
	for i := range x25519.LowOrderPoints {
		point := x25519.LowOrderPoints[i]
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

// scriptEphPeer plays the remote side of shareEphPubKey: it concurrently reads
// the local ephemeral key frame and writes the supplied ephemeral key.
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
