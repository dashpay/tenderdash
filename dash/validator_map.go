package dash

import (
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/types"
)

// validatorMap maps validator ID to the validator
type validatorMap map[p2p.ID]*types.Validator

// newValidatorMap creates a new validatoMap based on a slice of Validators
func newValidatorMap(validators []*types.Validator) validatorMap {
	newMap := make(validatorMap, len(validators))
	for _, validator := range validators {
		newMap[validator.NodeAddress.NodeID] = validator
	}

	return newMap
}

// values returns content (values) of the map as a slice
func (vm validatorMap) values() []*types.Validator {
	vals := make([]*types.Validator, 0, len(vm))
	for _, v := range vm {
		vals = append(vals, v)
	}
	return vals
}

// contains returns true if the validatorMap contains `What`, false otherwise.
// Items are compared using node ID.
func (vm validatorMap) contains(what *types.Validator) bool {
	_, ok := vm[what.NodeAddress.NodeID]
	return ok
}

// URIs returns URIs of all validators in this map
func (vm validatorMap) URIs() []string {
	uris := make([]string, 0, len(vm))
	for _, v := range vm {
		uris = append(uris, v.NodeAddress.String())
	}
	return uris
}
