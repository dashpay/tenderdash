package selectpeers

import "github.com/dashpay/tenderdash/types"

// ValidatorSelector represents an algorithm that chooses some validators from provided list
type ValidatorSelector interface {
	// SelectValidators selects some validators from `validators` slice
	SelectValidators(validators []*types.Validator, me *types.Validator) ([]*types.Validator, error)
	// SelectInboundValidators selects the validators that are expected to open a
	// connection to `me`, i.e. those for which `me` is a SelectValidators result.
	SelectInboundValidators(validators []*types.Validator, me *types.Validator) ([]*types.Validator, error)
}
