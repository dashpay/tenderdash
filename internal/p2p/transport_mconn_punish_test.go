package p2p

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto/ed25519"
	"github.com/dashpay/tenderdash/internal/libs/protoio"
	tmsync "github.com/dashpay/tenderdash/internal/libs/sync"
	"github.com/dashpay/tenderdash/internal/p2p/conn"
	"github.com/dashpay/tenderdash/libs/log"
	p2pproto "github.com/dashpay/tenderdash/proto/tendermint/p2p"
	"github.com/dashpay/tenderdash/types"
)

// newMConnWithRawRemote handshakes a real, started mConnConnection against a
// bare SecretConnection standing in for the remote peer, so a test can write
// arbitrary frames on the authenticated stream beneath MConnection.
func newMConnWithRawRemote(ctx context.Context, t *testing.T) (*mConnConnection, *conn.SecretConnection) {
	t.Helper()

	local, remote := net.Pipe()
	mc := newMConnConnection(log.NewNopLogger(), local, conn.DefaultMConnConfig(),
		[]*ChannelDescriptor{{ID: 1, Priority: 1}})
	t.Cleanup(func() { _ = mc.Close(); _ = remote.Close() })

	nodeInfo := func(id types.NodeID) types.NodeInfo {
		return types.NodeInfo{NodeID: id, ListenAddr: "0.0.0.0:0", Network: "test",
			Channels: tmsync.NewConcurrentSlice[uint16](0x01)}
	}
	localKey, remoteKey := ed25519.GenPrivKey(), ed25519.GenPrivKey()

	// Mirror mConnConnection.handshake on the remote side: secret connection,
	// then a NodeInfo exchange in both directions.
	secretCh := make(chan *conn.SecretConnection, 1)
	go func() {
		sc, err := conn.MakeSecretConnection(remote, remoteKey)
		if err != nil {
			secretCh <- nil
			return
		}
		written := make(chan struct{})
		go func() {
			defer close(written)
			_, _ = protoio.NewDelimitedWriter(sc).WriteMsg(nodeInfo(types.NodeIDFromPubKey(remoteKey.PubKey())).ToProto())
		}()
		var pb p2pproto.NodeInfo
		_, err = protoio.NewDelimitedReader(sc, types.MaxNodeInfoSize()).ReadMsg(&pb)
		<-written
		if err != nil {
			sc = nil
		}
		secretCh <- sc
	}()

	_, _, err := mc.Handshake(ctx, 10*time.Second, nodeInfo(types.NodeIDFromPubKey(localKey.PubKey())), localKey)
	require.NoError(t, err)
	sc := <-secretCh
	require.NotNil(t, sc, "remote handshake failed")
	return mc, sc
}

// A frame that arrives intact on the authenticated stream but does not decode
// is the sender's doing: it must end the connection and cost the peer score,
// the same as malformed bytes the router decodes itself.
func TestRoutePeer_MConnMalformedFrame_ScoresAndDisconnects(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	f := newPunishFixture(ctx, t, 10)
	f.enableRoutePeer()
	mc, remote := newMConnWithRawRemote(ctx, t)
	before := f.score()

	// Length prefix 2, then a Packet whose PacketPing field is truncated.
	go func() { _, _ = remote.Write([]byte{0x02, 0x0a, 0x05}) }()

	f.router.routePeer(ctx, f.peerID, mc, ChannelIDSet{1: {}}) // returns once the connection ended
	require.Equal(t, before-1, f.score(), "a malformed authenticated frame must cost exactly one penalty")
}

// A connection that drops mid-frame proves nothing about the peer, so the
// transport must not report it as invalid data.
func TestRoutePeer_MConnIOFailure_IsNotPunished(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	f := newPunishFixture(ctx, t, 10)
	f.enableRoutePeer()
	mc, remote := newMConnWithRawRemote(ctx, t)
	before := f.score()

	// Announce a 5-byte frame, deliver one byte, then hang up.
	_, err := remote.Write([]byte{0x05, 0x0a})
	require.NoError(t, err)
	require.NoError(t, remote.Close())

	f.router.routePeer(ctx, f.peerID, mc, ChannelIDSet{1: {}})
	require.NoError(t, mc.invalidDataErr())
	require.Equal(t, before, f.score(), "an I/O failure must not cost score")
}
