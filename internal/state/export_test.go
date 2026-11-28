package state

import (
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/types"
)

// ValidateValidatorUpdates is an alias for validateValidatorUpdates exported
// from execution.go, exclusively and explicitly for testing.
func ValidateValidatorUpdates(abciUpdates []abci.ValidatorUpdate, params types.ValidatorParams) error {
	return validateValidatorUpdates(abciUpdates, params)
}

// VerifyLastCommitBeacon is an alias for verifyLastCommitBeacon exported from validation.go, exclusively
// and explicitly for testing.
func VerifyLastCommitBeacon(state State, block *types.Block) error {
	return verifyLastCommitBeacon(state, block)
}
