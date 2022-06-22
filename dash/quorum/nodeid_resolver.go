package quorum

import (
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
func (resolver tcpNodeIDResolver) connect(host string, port uint16) (net.Conn, error) {
	dialer := net.Dialer{
		Timeout: resolver.DialerTimeout,
	}
	connection, err := dialer.Dial("tcp4", fmt.Sprintf("%s:%d", host, port))
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
func (resolver tcpNodeIDResolver) Resolve(va types.ValidatorAddress) (p2p.NodeAddress, error) {
	connection, err := resolver.connect(va.Hostname, va.Port)
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
func ResolveNodeID(va *types.ValidatorAddress, resolvers []p2p.NodeIDResolver, logger log.Logger) error {
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
		address, err := resolver.Resolve(*va)
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
