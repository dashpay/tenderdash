package selectpeers

import (
	"github.com/dashpay/tenderdash/types"
)

type allValidatorsSelector struct {
}

var _ ValidatorSelector = (*allValidatorsSelector)(nil)

// NewAllValidatorsSelector creates new implementation of validator selector algorithm
// that selects all validators except local node
func NewAllValidatorsSelector() ValidatorSelector {
	return &allValidatorsSelector{}
}

// SelectValidators implements ValidtorSelector.
// SelectValidators selects all validators from `validatorSetMembers`, except local node
func (s *allValidatorsSelector) SelectValidators(
	validatorSetMembers []*types.Validator,
	me *types.Validator,
) ([]*types.Validator, error) {
	ret := make([]*types.Validator, 0, len(validatorSetMembers)-1)
	for _, val := range validatorSetMembers {
		if !val.ProTxHash.Equal(me.ProTxHash) {
			ret = append(ret, val)
		}
	}
	return ret, nil
}
