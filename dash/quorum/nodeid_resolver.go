package quorum

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/rs/zerolog"

	"github.com/tendermint/tendermint/internal/p2p"
	"github.com/tendermint/tendermint/internal/p2p/conn"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/types"
)

const (
	// DefaultDialTimeout when resolving node id using TCP connection
	DefaultDialTimeout = 1000 * time.Millisecond
	// DefaultConnectionTimeout is a connection timeout when resolving node id using TCP connection
	DefaultConnectionTimeout = 1 * time.Second

	dnsLookupTimeout = 1 * time.Second
)

type tcpNodeIDResolver struct {
	DialerTimeout     time.Duration
	ConnectionTimeout time.Duration
}

// NewTCPNodeIDResolver creates new NodeIDResolver that connects to remote host with p2p protocol and
// derives node ID from remote p2p public key.
func NewTCPNodeIDResolver() p2p.NodeIDResolver {
	return &tcpNodeIDResolver{
		DialerTimeout:     DefaultDialTimeout,
		ConnectionTimeout: DefaultConnectionTimeout,
	}
}

// connect establishes a TCP connection to remote host.
// When err == nil, caller is responsible for closing of the connection
func (resolver tcpNodeIDResolver) connect(ctx context.Context, host string, port uint16) (net.Conn, error) {
	dialer := net.Dialer{
		Timeout: resolver.DialerTimeout,
	}

	connection, err := dialer.DialContext(ctx, "tcp4", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return nil, fmt.Errorf("cannot lookup node ID: %w", err)
	}
	if err := connection.SetDeadline(time.Now().Add(resolver.ConnectionTimeout)); err != nil {
		connection.Close()
		return nil, err
	}

	return connection, nil
}

// Resolve implements NodeIDResolver
// Resolve retrieves a node ID from remote validator and generates a correct node address.
// Note that it is quite expensive, as it establishes secure connection to the other node
// which is dropped afterwards.
func (resolver tcpNodeIDResolver) Resolve(ctx context.Context, va types.ValidatorAddress) (p2p.NodeAddress, error) {
	connection, err := resolver.connect(ctx, va.Hostname, va.Port)
	if err != nil {
		return p2p.NodeAddress{}, err
	}
	defer connection.Close()

	sc, err := conn.MakeSecretConnection(connection, nil)
	if err != nil {
		return p2p.NodeAddress{}, err
	}
	va.NodeID = types.NodeIDFromPubKey(sc.RemotePubKey())
	return nodeAddress(va), nil
}

// MarshalZerologObject implements zerolog.LogObjectMarshaler
func (resolver tcpNodeIDResolver) MarshalZerologObject(e *zerolog.Event) {
	e.Str("type", "tcpNodeIDResolver")
}

// ResolveNodeID adds node ID to the validator address if it's not set.
// If `resolvers` is empty or nil, it fefaults to tcpNodeIDResolver
func ResolveNodeID(ctx context.Context, va *types.ValidatorAddress, resolvers []p2p.NodeIDResolver, logger log.Logger) error {
	if len(resolvers) == 0 {
		resolvers = []p2p.NodeIDResolver{
			NewTCPNodeIDResolver(),
		}
	}

	if va.NodeID != "" {
		return nil
	}
	var allErrors error
	for index, resolver := range resolvers {
		address, err := resolver.Resolve(ctx, *va)
		if err == nil && address.NodeID != "" {
			va.NodeID = address.NodeID
			return nil // success
		}

		logger.Debug(
			"warning: validator node id lookup method failed",
			"url", va.String(),
			"index", index,
			"resolver", resolver,
			"error", err,
		)
		allErrors = multierror.Append(allErrors, fmt.Errorf("%d: %T error: %w", index, resolver, err))
	}
	return allErrors
}

type peerManagerResolver struct {
	peerManager *p2p.PeerManager
}

func NewPeerManagerResolver(peerManager *p2p.PeerManager) p2p.NodeIDResolver {
	return peerManagerResolver{peerManager: peerManager}
}

// Resolve implements NodeIDResolver.
// Resolve determines node ID using address book.
func (resolver peerManagerResolver) Resolve(ctx context.Context, va types.ValidatorAddress) (nodeAddress p2p.NodeAddress, err error) {
	ctx, cancel := context.WithTimeout(ctx, dnsLookupTimeout)
	defer cancel()

	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", va.Hostname)
	if err != nil {
		return nodeAddress, err
	}
	for _, ip := range ips {
		nodeAddress, err = resolver.lookupIPPort(ctx, ip, va.Port)
		// First match is returned
		if err == nil {
			return nodeAddress, nil
		}
	}
	return nodeAddress, err
}

// lookupIPPort searches through peer manager to determine which peer has given ip and port and returns its NodeAddress
func (resolver peerManagerResolver) lookupIPPort(ctx context.Context, ip net.IP, port uint16) (p2p.NodeAddress, error) {
	peers := resolver.peerManager.Peers()
	for _, nodeID := range peers {
		addresses := resolver.peerManager.Addresses(nodeID)
		if endpoint := findEndpointInAddresses(ctx, addresses, ip, port); endpoint != nil {
			return endpoint.NodeAddress(nodeID), nil
		}
	}

	return p2p.NodeAddress{}, p2p.ErrPeerNotFound(fmt.Errorf("peer %s:%d not found in the address book", ip, port))
}

// findEndpointInAddresses returns endpoint with provided ip and port from the list of addresses, or nil if not found
func findEndpointInAddresses(ctx context.Context, addresses []p2p.NodeAddress, ip net.IP, port uint16) *p2p.Endpoint {
	for _, addr := range addresses {
		endpoints, err := addr.Resolve(ctx)
		if err != nil {
			continue // ignore invalid address
		}
		if endpoint := findEndpoint(endpoints, ip, port); endpoint != nil {
			return endpoint
		}
	}

	return nil
}

// findEndpoint returns endpoint with provided ip and port in the list of endpoints, or nil if not found
func findEndpoint(endpoints []*p2p.Endpoint, ip net.IP, port uint16) *p2p.Endpoint {
	for _, item := range endpoints {
		if item.IP.Equal(ip) && item.Port == port {
			return item
		}
	}

	return nil
}
