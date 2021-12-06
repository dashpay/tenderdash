package types

import (
	"fmt"
	"net"
	"time"

	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/p2p/conn"
)

const (
	DefaultDialTimeout       = 1000 * time.Millisecond
	DefaultConnectionTimeout = 1 * time.Second
)

type NodeIDResolver interface {
	// Resolve retrieves a node ID from remote node.
	Resolve(ValidatorAddress) (p2p.ID, error)
}

type nodeIDResolver struct {
	DialerTimeout     time.Duration
	ConnectionTimeout time.Duration
	// other dependencies
}

func NewNodeIDResolver() NodeIDResolver {
	return &nodeIDResolver{
		DialerTimeout:     DefaultDialTimeout,
		ConnectionTimeout: DefaultConnectionTimeout,
	}
}

// connect establishes a TCP connection to remote host.
// When err == nil, caller is responsible for closing of the connection
func (resolver nodeIDResolver) connect(host string, port uint16) (net.Conn, error) {
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

// Resolve implements NodeIDResolver
// Resolve retrieves a node ID from remote node.
// Note that it is quite expensive, as it establishes secure connection to the other node
// which is dropped afterwards.
func (resolver nodeIDResolver) Resolve(va ValidatorAddress) (p2p.ID, error) {
	connection, err := resolver.connect(va.Hostname(), va.Port())
	if err != nil {
		return "", err
	}
	defer connection.Close()

	sc, err := conn.MakeSecretConnection(connection, nil)
	if err != nil {
		return "", err
	}
	return p2p.PubKeyToID(sc.RemotePubKey()), nil
}
