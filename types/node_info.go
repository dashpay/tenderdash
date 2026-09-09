package types

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/dashpay/tenderdash/crypto"
	tmstrings "github.com/dashpay/tenderdash/internal/libs/strings"
	tmsync "github.com/dashpay/tenderdash/internal/libs/sync"
	tmp2p "github.com/dashpay/tenderdash/proto/tendermint/p2p"
)

const (
	maxNodeInfoSize = 10240 // 10KB
	maxNumChannels  = 16    // plenty of room for upgrades, for now
)

// Max size of the NodeInfo struct
func MaxNodeInfoSize() int {
	return maxNodeInfoSize
}

// ProtocolVersion contains the protocol versions for the software.
type ProtocolVersion struct {
	P2P   uint64 `json:"p2p,string"`
	Block uint64 `json:"block,string"`
	App   uint64 `json:"app,string"`
}

//-------------------------------------------------------------

// NodeInfo is the basic node information exchanged
// between two peers during the Tendermint P2P handshake.
type NodeInfo struct {
	ProtocolVersion ProtocolVersion `json:"protocol_version"`

	// Authenticate
	NodeID     NodeID `json:"id"`          // authenticated identifier
	ListenAddr string `json:"listen_addr"` // accepting incoming

	// Node Type
	ProTxHash crypto.ProTxHash

	// Check compatibility.
	// Channels are HexBytes so easier to read as JSON
	Network string `json:"network"` // network/chain ID
	Version string `json:"version"` // major.minor.revision
	// Channels supported by this node. Use GetChannels() as a getter.
	Channels *tmsync.ConcurrentSlice[uint16] `json:"channels"` // channels this node knows about

	// ASCIIText fields
	Moniker string        `json:"moniker"` // arbitrary moniker
	Other   NodeInfoOther `json:"other"`   // other application specific data
}

// NodeInfoOther is the misc. applcation specific data
type NodeInfoOther struct {
	TxIndex    string `json:"tx_index"`
	RPCAddress string `json:"rpc_address"`
}

// ID returns the node's peer ID.
func (info NodeInfo) ID() NodeID {
	return info.NodeID
}

func (info NodeInfo) GetProTxHash() crypto.ProTxHash {
	return info.ProTxHash.Copy()
}

// Validate checks the self-reported DefaultNodeInfo is safe.
// It returns an error if there
// are too many Channels, if there are any duplicate Channels,
// or if the ListenAddr is malformed.
// TODO: constraints for Moniker/Other? Or is that for the UI ?
// JAE: It needs to be done on the client, but to prevent ambiguous
// unicode characters, maybe it's worth sanitizing it here.
// In the future we might want to validate these, once we have a
// name-resolution system up.
// International clients could then use punycode (or we could use
// url-encoding), and we just need to be careful with how we handle that in our
// clients. (e.g. off by default).
func (info NodeInfo) Validate() error {
	// Deliberately does not resolve: ListenAddr comes from an unauthenticated peer
	// and this runs after conn.Handshake returns, outside HandshakeTimeout.
	// INTENTIONAL(ipv4-literal-deployments): the RFC 1123 rule rejects underscore,
	// IPv6-zone and IDN hostnames, whether a peer reports them or an operator
	// configures them (node.makeNodeInfo and node.makeSeedNodeInfo both validate
	// through here). Accepted: deployments address nodes by a single IPv4 literal.
	if err := validateAddressString(info.ID().AddressString(info.ListenAddr)); err != nil {
		return err
	}

	// Validate Version
	if len(info.Version) > 0 {
		if ver, err := tmstrings.ASCIITrim(info.Version); err != nil || ver == "" {
			return fmt.Errorf("info.Version must be valid ASCII text without tabs, but got, %q [%s]", info.Version, ver)
		}
	}

	// Validate Channels - ensure max and check for duplicates.
	if info.Channels == nil {
		return fmt.Errorf("info.Channels is nil")
	}

	if info.Channels.Len() > maxNumChannels {
		return fmt.Errorf("info.Channels is too long (%v). Max is %v", info.Channels.Len(), maxNumChannels)
	}
	channels := make(map[uint16]struct{})
	for _, ch := range info.Channels.ToSlice() {
		_, ok := channels[ch]
		if ok {
			return fmt.Errorf("info.Channels contains duplicate channel id %v", ch)
		}
		channels[ch] = struct{}{}
	}

	if m, err := tmstrings.ASCIITrim(info.Moniker); err != nil || m == "" {
		return fmt.Errorf("info.Moniker must be valid non-empty ASCII text without tabs, but got %v", info.Moniker)
	}

	// Validate Other.
	other := info.Other
	txIndex := other.TxIndex
	switch txIndex {
	case "", "on", "off":
	default:
		return fmt.Errorf("info.Other.TxIndex should be either 'on', 'off', or empty string, got '%v'", txIndex)
	}
	// XXX: Should we be more strict about address formats?
	rpcAddr := other.RPCAddress
	if len(rpcAddr) > 0 {
		if a, err := tmstrings.ASCIITrim(rpcAddr); err != nil || a == "" {
			return fmt.Errorf("info.Other.RPCAddress=%v must be valid ASCII text without tabs", rpcAddr)
		}
	}

	return nil
}

// CompatibleWith checks if two NodeInfo are compatible with each other.
// CONTRACT: two nodes are compatible if the Block version and network match
// and they have at least one channel in common.
func (info NodeInfo) CompatibleWith(other NodeInfo) error {
	if info.ProtocolVersion.Block != other.ProtocolVersion.Block {
		return fmt.Errorf("peer is on a different Block version. Got %v, expected %v",
			other.ProtocolVersion.Block, info.ProtocolVersion.Block)
	}

	// nodes must be on the same network
	if info.Network != other.Network {
		return fmt.Errorf("peer is on a different network. Got %v, expected %v", other.Network, info.Network)
	}

	// if we have no channels, we're just testing
	if info.Channels.Len() == 0 {
		return nil
	}

	// for each of our channels, check if they have it
	found := false
OUTER_LOOP:
	for _, ch1 := range info.Channels.ToSlice() {
		for _, ch2 := range other.Channels.ToSlice() {
			if ch1 == ch2 {
				found = true
				break OUTER_LOOP // only need one
			}
		}
	}
	if !found {
		return fmt.Errorf("peer has no common channels. Our channels: %v ; Peer channels: %v", info.Channels, other.Channels)
	}
	return nil
}

// AddChannel is used by the router when a channel is opened to add it to the node info
func (info *NodeInfo) AddChannel(channel uint16) {
	// check that the channel doesn't already exist
	for _, ch := range info.Channels.ToSlice() {
		if ch == channel {
			return
		}
	}

	info.Channels.Append(channel)
}

func (info NodeInfo) Copy() NodeInfo {
	chans := info.Channels.Copy()
	return NodeInfo{
		ProtocolVersion: info.ProtocolVersion,
		NodeID:          info.NodeID,
		ListenAddr:      info.ListenAddr,
		Network:         info.Network,
		Version:         info.Version,
		Channels:        &chans,
		Moniker:         info.Moniker,
		Other:           info.Other,
		ProTxHash:       info.ProTxHash.Copy(),
	}
}

func (info NodeInfo) ToProto() *tmp2p.NodeInfo {

	dni := new(tmp2p.NodeInfo)
	dni.ProtocolVersion = tmp2p.ProtocolVersion{
		P2P:   info.ProtocolVersion.P2P,
		Block: info.ProtocolVersion.Block,
		App:   info.ProtocolVersion.App,
	}

	for _, ch := range info.Channels.ToSlice() {
		dni.Channels = append(dni.Channels, uint32(ch))
	}

	dni.NodeID = string(info.NodeID)
	dni.ListenAddr = info.ListenAddr
	dni.Network = info.Network
	dni.Version = info.Version
	dni.Moniker = info.Moniker
	dni.ProTxHash = info.ProTxHash.Copy()
	dni.Other = tmp2p.NodeInfoOther{
		TxIndex:    info.Other.TxIndex,
		RPCAddress: info.Other.RPCAddress,
	}

	return dni
}

func NodeInfoFromProto(pb *tmp2p.NodeInfo) (NodeInfo, error) {
	if pb == nil {
		return NodeInfo{}, errors.New("nil node info")
	}
	dni := NodeInfo{
		ProtocolVersion: ProtocolVersion{
			P2P:   pb.ProtocolVersion.P2P,
			Block: pb.ProtocolVersion.Block,
			App:   pb.ProtocolVersion.App,
		},
		NodeID:     NodeID(pb.NodeID),
		ListenAddr: pb.ListenAddr,
		Network:    pb.Network,
		Version:    pb.Version,
		Channels:   tmsync.NewConcurrentSlice[uint16](),
		Moniker:    pb.Moniker,
		Other: NodeInfoOther{
			TxIndex:    pb.Other.TxIndex,
			RPCAddress: pb.Other.RPCAddress,
		},
		ProTxHash: pb.ProTxHash,
	}

	for _, ch := range pb.Channels {
		dni.Channels.Append(uint16(ch))
	}

	return dni, nil
}

// ParseAddressString reads an address string, and returns the NetAddress struct
// with ip address, port and nodeID information, returning an error for any validation
// errors. The host must pass ValidateHostname before it is resolved.
func ParseAddressString(addr string) (*NetAddress, error) {
	id, host, port, err := splitAddressString(addr)
	if err != nil {
		return nil, err
	}
	if err := ValidateHostname(host); err != nil {
		return nil, err
	}

	ip := net.ParseIP(host)
	if ip == nil {
		ips, err := net.LookupIP(host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("no IP address found for host %q", host)
		}
		ip = ips[0]
	}

	na := NewNetAddressIPPort(ip, port)
	na.ID = id

	return na, nil
}

// splitAddressString splits an address string into its node ID, host and port,
// validating the node ID and port. The host is checked only for emptiness; the
// caller decides whether to validate or resolve it.
func splitAddressString(addr string) (id NodeID, host string, port uint16, err error) {
	spl := strings.Split(removeProtocolIfDefined(addr), "@")
	if len(spl) != 2 {
		return "", "", 0, errors.New("invalid address")
	}

	id, err = NewNodeID(spl[0])
	if err != nil {
		return "", "", 0, err
	}
	if err = id.Validate(); err != nil {
		return "", "", 0, err
	}

	host, portStr, err := net.SplitHostPort(spl[1])
	if err != nil {
		return "", "", 0, err
	}
	if len(host) == 0 {
		return "", "", 0, fmt.Errorf("invalid address %q: empty host", addr)
	}

	parsedPort, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return "", "", 0, err
	}

	return id, host, uint16(parsedPort), nil
}

// validateAddressString checks an address string without resolving its host.
// Unlike ParseAddressString it never performs a DNS lookup, so it is safe to run on
// input supplied by an unauthenticated peer.
func validateAddressString(addr string) error {
	_, host, _, err := splitAddressString(addr)
	if err != nil {
		return err
	}
	return ValidateHostname(host)
}

func removeProtocolIfDefined(addr string) string {
	if strings.Contains(addr, "://") {
		return strings.Split(addr, "://")[1]
	}
	return addr

}
