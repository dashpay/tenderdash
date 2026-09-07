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

// LastCommitVerified exposes lastCommitVerified for tests of the verified-commit
// memo.
func (blockExec *BlockExecutor) LastCommitVerified(state State, block *types.Block) bool {
	return blockExec.lastCommitVerified(state, block)
}
