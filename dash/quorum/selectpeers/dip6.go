// Package selectpeers is package contains algorithm that selects peers based on the deterministic connection
// selection algorithm described in DIP-6
package selectpeers

import (
	"fmt"
	"math"

	"github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/types"
)

// minValidators is a minimum number of validators needed in order to execute the selection
// algorithm. For less than this number, we connect to all validators.
const minValidators = 5

// connectionDirection is the direction in which the DIP-6 overlay is walked: the
// connections a member opens, or the ones opened to it.
type connectionDirection int

const (
	outgoing connectionDirection = 1
	incoming connectionDirection = -1
)

// DIP6 selector selects validators from the `validatorSetMembers`, based on algorithm
// described in DIP-6 https://github.com/dashpay/dips/blob/master/dip-0006.md
type dip6PeerSelector struct {
	quorumHash bytes.HexBytes
}

// NewDIP6ValidatorSelector creates new implementation of validator selector algorithm
func NewDIP6ValidatorSelector(quorumHash bytes.HexBytes) ValidatorSelector {
	return &dip6PeerSelector{quorumHash: quorumHash}
}

// SelectValidators implements ValidtorSelector.
// SelectValidators selects some validators from `validatorSetMembers`, according to the algorithm
// described in DIP-6 https://github.com/dashpay/dips/blob/master/dip-0006.md
func (s *dip6PeerSelector) SelectValidators(
	validatorSetMembers []*types.Validator,
	me *types.Validator,
) ([]*types.Validator, error) {
	return s.selectNeighbours(validatorSetMembers, me, outgoing)
}

// SelectInboundValidators implements ValidatorSelector.
//
// The DIP-6 overlay is directed: a member at index i connects to (i+2^k)%n, so
// the members that connect to it are the ones at (i-2^k)%n, a different set. A
// node that wants to hold on to its whole DIP-6 neighbourhood therefore has to
// account for both, since it only ever dials one of the two halves.
func (s *dip6PeerSelector) SelectInboundValidators(
	validatorSetMembers []*types.Validator,
	me *types.Validator,
) ([]*types.Validator, error) {
	return s.selectNeighbours(validatorSetMembers, me, incoming)
}

// selectNeighbours returns the DIP-6 neighbours of `me` in one direction of the
// overlay.
func (s *dip6PeerSelector) selectNeighbours(
	validatorSetMembers []*types.Validator,
	me *types.Validator,
	direction connectionDirection,
) ([]*types.Validator, error) {
	if len(validatorSetMembers) < 2 {
		return nil, fmt.Errorf("not enough validators: got %d, need 2", len(validatorSetMembers))
	}
	// Build the deterministic list of quorum members:
	// 1. Retrieve the deterministic masternode list which is valid at quorumHeight
	// 2. Calculate SHA256(proTxHash, quorumHash) for each entry in the list
	// 3. Sort the resulting list by the calculated hashes
	sortedValidators := newSortedValidatorList(validatorSetMembers, s.quorumHash)

	// Loop through the list until the member finds itself in the list. The index at which it finds itself is called i.
	meSortable := newSortableValidator(*me, s.quorumHash)
	myIndex := sortedValidators.index(meSortable)
	if myIndex < 0 {
		return []*types.Validator{}, fmt.Errorf("current node is not a member of provided validator set")
	}

	// Fallback if we don't have enough validators, we connect to all of them
	if sortedValidators.Len() < minValidators {
		ret := make([]*types.Validator, 0, len(validatorSetMembers)-1)
		// We connect to all validators
		for index, val := range sortedValidators {
			if index != myIndex {
				ret = append(ret, val.Copy())
			}
		}
		return ret, nil
	}

	// Calculate indexes (i+2^k)%n where k is in the range 0..floor(log2(n-1))-1
	// and n is equal to the size of the list. The inbound direction walks the
	// same offsets backwards, which is what makes the two selections inverses.
	n := sortedValidators.Len()
	count := int(math.Floor(math.Log2(float64(n)-1.0))) - 1

	ret := make([]*types.Validator, 0, count+1)
	for k := 0; k <= count; k++ {
		offset := int(direction) * (1 << uint(k))
		// Go's % keeps the sign of the dividend, so bring it back into range.
		index := ((myIndex+offset)%n + n) % n
		// Add addresses of masternodes at indexes calculated at previous step
		// to the set of deterministic connections.
		ret = append(ret, sortedValidators[index].Validator.Copy())
	}

	return ret, nil
}
