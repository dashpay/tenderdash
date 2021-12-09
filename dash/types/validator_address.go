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
}

var (
	ErrNoHostname error = errors.New("no hostname")
	ErrNoPort     error = errors.New("no port")
	ErrNoProtocol error = errors.New("no protocol")
	ErrNoResolver error = errors.New("resolver not defined, validator address not initialized correctly")
	ErrNoNodeID   error = errors.New("no node ID")
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
	if len(va.NodeAddress.NodeID) > 0 {
		if err := p2p.ValidateID(va.NodeAddress.NodeID); err != nil {
			return err
		}
	}

	return nil
}

//  NetAddress returns this ValidatorAddress as a *p2p.NetAddress that can be used to establish connection
func (va ValidatorAddress) NetAddress() (*p2p.NetAddress, error) {
	if va.NodeID == "" {
		return nil, fmt.Errorf("cannot determine node id for address %s", va.String())
	}
	return va.NodeAddress.NetAddress()
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
