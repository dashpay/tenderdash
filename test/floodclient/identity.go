//go:build floodclient

// Package floodclient is a p2p-level consensus flood tool for stress-testing a
// Tenderdash node's DoS defences on a controlled network. It speaks the real
// peer handshake (SecretConnection + NodeInfo exchange) as an ordinary peer and
// sends structurally-valid but cryptographically-forged consensus messages on
// the consensus channels.
//
// It is guarded behind the "floodclient" build tag so it can never be linked
// into the production node binary or a release image.
package floodclient

import (
	tmsync "github.com/dashpay/tenderdash/internal/libs/sync"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/types"
	"github.com/dashpay/tenderdash/version"
)

// Identity is a single peer identity used by the flood client. Node IDs are
// free (hex(SHA256(ed25519 pubkey))), so a run can mint as many as it needs to
// hold connection slots on the target.
type Identity struct {
	NodeKey types.NodeKey
}

// NewIdentity mints a fresh ed25519 identity.
func NewIdentity() Identity {
	return Identity{NodeKey: types.GenNodeKey()}
}

// NewIdentities mints n fresh identities.
func NewIdentities(n int) []Identity {
	ids := make([]Identity, n)
	for i := range ids {
		ids[i] = NewIdentity()
	}
	return ids
}

// NodeInfo builds the NodeInfo this identity presents during the handshake. It
// must be compatible with the target: the router only admits a peer whose
// ProtocolVersion.Block and Network match, so both are taken from this build's
// compile-time constants and the caller-supplied chain ID.
//
// The advertised channels are the four consensus channels; that is all a flood
// client needs to be routed consensus traffic. listenAddr is only advertised
// (the client does not listen); it must parse as a valid address.
func (id Identity) NodeInfo(network, listenAddr string) types.NodeInfo {
	return types.NodeInfo{
		ProtocolVersion: types.ProtocolVersion{
			P2P:   version.P2PProtocol,
			Block: version.BlockProtocol,
			App:   0,
		},
		NodeID:  id.NodeKey.ID,
		Network: network,
		Version: version.TMCoreSemVer,
		Channels: tmsync.NewConcurrentSlice[uint16](
			uint16(p2p.ConsensusStateChannel),
			uint16(p2p.ConsensusDataChannel),
			uint16(p2p.ConsensusVoteChannel),
			uint16(p2p.VoteSetBitsChannel),
		),
		Moniker:    "floodclient",
		Other:      types.NodeInfoOther{TxIndex: "off"},
		ListenAddr: listenAddr,
	}
}
