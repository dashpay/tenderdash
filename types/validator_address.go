package types

import (
	"fmt"

	tmrand "github.com/tendermint/tendermint/libs/rand"
	"github.com/tendermint/tendermint/p2p"
)

// ValidatorAddress represent an address of a validator
type ValidatorAddress p2p.NodeAddress

// NewValidatorAddress parses validator address according to thre URI specification (RFC 3986).
// If the uri does not contain protocol part, it defaults to `tcp://`.
func NewValidatorAddress(uri string) (ValidatorAddress, error) {
	if uri == "" {
		return ValidatorAddress{}, nil
	}

	nodeAddress, err := p2p.ParseNodeAddress(uri)
	return ValidatorAddress(nodeAddress), err
}

// RandValidatorAddress generates a random validator address
func RandValidatorAddress() ValidatorAddress {
	nodeID := tmrand.Bytes(20)
	port := (tmrand.Int() % 65535) + 1
	addr, err := NewValidatorAddress(fmt.Sprintf("tcp://%x@127.0.0.1:%d", nodeID, port))
	if err != nil {
		panic(fmt.Sprintf("cannot generate random validator address: %s", err))
	}
	if err := addr.Validate(); err != nil {
		panic(fmt.Sprintf("randomly generated validator address %s is invalid: %s", addr.String(), err))
	}
	return addr
}

// NodeAddress converts validator address to a node address that can be used to establish a connection.
func (a ValidatorAddress) NodeAddress() p2p.NodeAddress {
	return p2p.NodeAddress(a)
}

// NetAddress converts validator address to a NetAddress struct that can be used to establish a connection.
func (a ValidatorAddress) NetAddress() (*p2p.NetAddress, error) {
	addr, err := p2p.NewNetAddressString(a.String())
	if err != nil || addr == nil {
		return nil, err
	}
	return addr, nil
}

// String converts the validator address to a string representation of the URI.
func (a ValidatorAddress) String() string {
	return p2p.NodeAddress(a).String()
}

// Validate validates the address and returns error if it's not valid.
func (a ValidatorAddress) Validate() error {
	return p2p.NodeAddress(a).Validate()
}
