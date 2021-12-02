package types

import (
	"errors"
	"fmt"
	"math"

	tmrand "github.com/tendermint/tendermint/libs/rand"
	"github.com/tendermint/tendermint/p2p"
)

// ValidatorAddress is a NodeAddress that does not require node ID to be set
type ValidatorAddress struct {
	p2p.NodeAddress
	resolver NodeIDResolver
}

var (
	ErrNoHostname error = errors.New("no hostname")
	ErrNoPort     error = errors.New("no port")
	ErrNoProtocol error = errors.New("no protocol")
	ErrNoResolver error = errors.New("resolver not defined, validator address not initialized correctly")
)

// ParseValidatorAddress parses provided address, which should be in `proto://nodeID@host:port` form.
// `proto://` and `nodeID@` parts are optional.
func ParseValidatorAddress(address string) (ValidatorAddress, error) {
	addr, err := p2p.ParseNodeAddressWithoutValidation(address)
	if err != nil {
		return ValidatorAddress{}, err
	}
	va := ValidatorAddress{
		NodeAddress: addr,
		resolver:    NewNodeIDResolver(),
	}
	return va, va.Validate()
}

// Validate ensures the validator address is correct.
// It ignores missing node IDs.
func (va ValidatorAddress) Validate() error {
	if va.NodeAddress.Protocol == "" {
		return ErrNoProtocol
	}
	if va.NodeAddress.Hostname == "" {
		return ErrNoHostname
	}
	if va.NodeAddress.Port <= 0 {
		return ErrNoPort
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

// Protocol returns protocol name of this address, like "tcp"
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

		if va.resolver == nil {
			return "", ErrNoResolver
		}

		va.NodeAddress.NodeID, err = va.resolver.Resolve(*va)
		if err != nil {
			return "", err
		}
	}
	return va.NodeAddress.NodeID, nil
}

// RandValidatorAddress generates a random validator address. Used in tests.
func RandValidatorAddress() ValidatorAddress {
	nodeID := tmrand.Bytes(20)
	port := tmrand.Int()%math.MaxUint16 + 1
	addr, err := ParseValidatorAddress(fmt.Sprintf("tcp://%x@127.0.0.1:%d", nodeID, port))
	if err != nil {
		panic(fmt.Sprintf("cannot generate random validator address: %s", err))
	}
	if err := addr.Validate(); err != nil {
		panic(fmt.Sprintf("randomly generated validator address %s is invalid: %s", addr.String(), err))
	}
	return addr
}
