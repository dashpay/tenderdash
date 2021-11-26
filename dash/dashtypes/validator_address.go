package dashtypes

import (
	"errors"
	"fmt"
	"net"
	"time"

	tmrand "github.com/tendermint/tendermint/libs/rand"
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/p2p/conn"
)

// ValidatorAddress is a NodeAddress that does not require node ID to be set
type ValidatorAddress struct {
	p2p.NodeAddress
}

// ParseValidatorAddress parses provided address, which should be in `proto://nodeID@host:port` form.
// `proto://` and `nodeID@` parts are optional.
func ParseValidatorAddress(address string) (ValidatorAddress, error) {
	addr, err := p2p.ParseNodeAddressWithoutValidation(address)
	if err != nil {
		return ValidatorAddress{}, err
	}
	va := ValidatorAddress{NodeAddress: addr}
	return va, va.Validate()
}

// Validate ensures the validator address is correct.
// It ignores missing node IDs.
func (va ValidatorAddress) Validate() error {
	if va.NodeAddress.Protocol == "" {
		return errors.New("no protocol")
	}
	if va.NodeAddress.Hostname == "" {
		return errors.New("no hostname")
	}
	if va.NodeAddress.Port <= 0 {
		return errors.New("no port")
	}
	return nil
}

// Hostname returns host name of this address
func (va ValidatorAddress) Hostname() string {
	return va.NodeAddress.Hostname
}

// Port returns port number of this address
func (va ValidatorAddress) Port() uint16 {
	return va.NodeAddress.Port
}

// Protocol returns protocl name of this address, like "tcp"
func (va ValidatorAddress) Protocol() string {
	return va.NodeAddress.Protocol
}

//  NetAddress returns this ValidatorAddress as a *p2p.NetAddress that can be used to establish connection
func (va ValidatorAddress) NetAddress() (*p2p.NetAddress, error) {
	if _, err := va.NodeID(); err != nil {
		return nil, fmt.Errorf("cannot determine node id for address %s: %w", va.String(), err)
	}
	return va.NodeAddress.NetAddress()
}

// NodeID() returns node ID. If it is not set, it will connect to remote node, retrieve its public key
// and calculate Node ID based on it. Noe this connection can be expensive.
func (va *ValidatorAddress) NodeID() (p2p.ID, error) {
	if va.NodeAddress.NodeID == "" {
		var err error
		va.NodeAddress.NodeID, err = va.retrieveNodeID()
		if err != nil {
			return "", err
		}
	}
	return va.NodeAddress.NodeID, nil
}

// retrieveNodeID retrieves a node ID from remote node.
// Note that it is quite expensive, as it establishes secure connection to the other node
// which is dropped afterwards.
func (va ValidatorAddress) retrieveNodeID() (p2p.ID, error) {
	dialer := net.Dialer{Timeout: 1000 * time.Millisecond}
	connection, err := dialer.Dial("tcp4", fmt.Sprintf("%s:%d", va.NodeAddress.Hostname, va.NodeAddress.Port))
	if err != nil {
		return "", fmt.Errorf("cannot lookup node ID: %w", err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(1 * time.Second)); err != nil {
		return "", err
	}

	sc, err := conn.MakeSecretConnection(connection, nil)
	if err != nil {
		return "", err
	}
	return p2p.PubKeyToID(sc.RemotePubKey()), nil
}

// dashtypes.RandValidatorAddress generates a random validator address
func RandValidatorAddress() ValidatorAddress {
	nodeID := tmrand.Bytes(20)
	port := (tmrand.Int() % 65535) + 1
	addr, err := ParseValidatorAddress(fmt.Sprintf("tcp://%x@127.0.0.1:%d", nodeID, port))
	if err != nil {
		panic(fmt.Sprintf("cannot generate random validator address: %s", err))
	}
	if err := addr.Validate(); err != nil {
		panic(fmt.Sprintf("randomly generated validator address %s is invalid: %s", addr.String(), err))
	}
	return addr
}
