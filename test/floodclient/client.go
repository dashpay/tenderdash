//go:build floodclient

package floodclient

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/cosmos/gogoproto/proto"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/conn"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

// Target identifies the node to flood.
type Target struct {
	// Host is an IP or hostname of the target node's p2p listener.
	Host string
	// Port is the target node's p2p port.
	Port uint16
	// NodeID, if set, is checked against the node ID returned by the target's
	// handshake; a mismatch fails the dial (guards against flooding the wrong
	// node). Empty skips the check.
	NodeID types.NodeID
	// Network is the target's chain ID. Must match or the target rejects the
	// peer as incompatible.
	Network string
	// MaxPacketPayload caps the payload of each MConnection packet this client
	// emits. A message larger than the target's own max-packet-msg-payload-size
	// is split into packets of this size; if they exceed the target's limit the
	// target rejects the frame mid-read and drops the connection, so this must be
	// set at or below the target's limit. Zero uses the protocol default (1400),
	// which is safe only against a target whose limit is >= 1400.
	MaxPacketPayload int
}

// Conn is a single flood connection: one identity, one handshaked link to the
// target, over which forged consensus messages are sent.
type Conn struct {
	transport *p2p.MConnTransport
	conn      p2p.Connection
	identity  Identity
	peerInfo  types.NodeInfo
}

// consensusChannelSlice returns the four consensus channel descriptors as a
// slice, reusing the production definitions so the MConnection frames traffic
// exactly as a real peer does.
func consensusChannelSlice() []*p2p.ChannelDescriptor {
	m := p2p.ConsensusChannelDescriptors()
	descs := make([]*p2p.ChannelDescriptor, 0, len(m))
	for _, d := range m {
		descs = append(descs, d)
	}
	return descs
}

// Dial opens a connection to the target and completes the real peer handshake
// (authenticated ed25519 SecretConnection + NodeInfo exchange) as identity.
func Dial(
	ctx context.Context,
	logger log.Logger,
	identity Identity,
	target Target,
	handshakeTimeout time.Duration,
) (*Conn, error) {
	ips, err := net.LookupIP(target.Host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("resolve target host %q: %w", target.Host, err)
	}

	mconnCfg := conn.DefaultMConnConfig()
	if target.MaxPacketPayload > 0 {
		mconnCfg.MaxPacketMsgPayloadSize = target.MaxPacketPayload
	}
	transport := p2p.NewMConnTransport(
		logger,
		mconnCfg,
		consensusChannelSlice(),
		p2p.MConnTransportOptions{},
	)

	endpoint := &p2p.Endpoint{Protocol: p2p.MConnProtocol, IP: ips[0], Port: target.Port}
	c, err := transport.Dial(ctx, endpoint)
	if err != nil {
		_ = transport.Close()
		return nil, fmt.Errorf("dial %s: %w", endpoint, err)
	}

	nodeInfo := identity.NodeInfo(target.Network, "127.0.0.1:26656")
	peerInfo, peerKey, err := c.Handshake(ctx, handshakeTimeout, nodeInfo, identity.NodeKey.PrivKey)
	if err != nil {
		_ = c.Close()
		_ = transport.Close()
		return nil, fmt.Errorf("handshake: %w", err)
	}

	if types.NodeIDFromPubKey(peerKey) != peerInfo.NodeID {
		_ = c.Close()
		_ = transport.Close()
		return nil, fmt.Errorf("target key/ID mismatch: key=%s id=%s",
			types.NodeIDFromPubKey(peerKey), peerInfo.NodeID)
	}
	if target.NodeID != "" && peerInfo.NodeID != target.NodeID {
		_ = c.Close()
		_ = transport.Close()
		return nil, fmt.Errorf("connected to %s, expected %s", peerInfo.NodeID, target.NodeID)
	}

	return &Conn{transport: transport, conn: c, identity: identity, peerInfo: peerInfo}, nil
}

// PeerInfo returns the target's NodeInfo as learned during the handshake.
func (c *Conn) PeerInfo() types.NodeInfo { return c.peerInfo }

// NodeID returns this connection's own (attacker) node ID.
func (c *Conn) NodeID() types.NodeID { return c.identity.NodeKey.ID }

// Send marshals msg into a p2p envelope and writes it on the channel implied by
// its type (via the production ResolveChannelID mapping), producing bytes
// byte-identical to what a real peer would put on the wire.
func (c *Conn) Send(ctx context.Context, msg proto.Message) error {
	chID := p2p.ResolveChannelID(msg)
	env := p2p.Envelope{ChannelID: chID, Message: msg}
	pb, err := env.ToProto()
	if err != nil {
		return fmt.Errorf("wrap envelope: %w", err)
	}
	bz, err := proto.Marshal(pb)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	return c.conn.SendMessage(ctx, chID, bz)
}

// Close tears down the connection and its transport.
func (c *Conn) Close() error {
	_ = c.conn.Close()
	return c.transport.Close()
}
