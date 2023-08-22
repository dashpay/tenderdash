package selectpeers

import "github.com/dashpay/tenderdash/types"

// ValidatorSelector represents an algorithm that chooses some validators from provided list
type ValidatorSelector interface {
	// SelectValidators selects some validators from `validators` slice
	SelectValidators(validators []*types.Validator, me *types.Validator) ([]*types.Validator, error)
}
