package validatorscoring

import (
	"github.com/dashpay/tenderdash/types"
)

type ValidatorScoringStrategy interface {
	// GetProposer returns the proposer for the given height and round. It calls Update if necessary.
	GetProposer(height int64, round int32) (*types.Validator, error)

	// MustGetProposer returns the proposer for the given height and round. It calls Update if necessary.
	// It panics on any error.
	//
	// Use in tests
	// See [GetProposer](#GetProposer) for details.
	MustGetProposer(height int64, round int32) *types.Validator
	// Update updates scores for the given height and round. It should be called at least once for each round.
	//   - `height` is the height
	//  - `round` is the round
	UpdateScores(height int64, round int32) error
	// Returns pointer to underlying validator set; not thread-safe, and should be modified with caution
	ValidatorSet() *types.ValidatorSet
	// Create deep copy of the strategy and its underlying validator set
	Copy() ValidatorScoringStrategy
}

// NewValidatorScoringStrategy creates a ValidatorScoringStrategy from the ValidatorSet.
// Original ValidatorSet should not be used anymore. Height and round should point to the current height and round,
// eg. the one for which GetProposer() returns proposer that generates proposal at the given height and round.
//
// ## Arguments
//
// - `cp` - ConsensusParams that determine scoring strategy to use
// - `height` - current height of the validator set
// - `round` - current round of the validator set
func NewValidatorScoringStrategy(cp types.ConsensusParams, valSet *types.ValidatorSet, currentHeight int64, currentRound int32,
	bs BlockCommitStore) (ValidatorScoringStrategy, error) {
	return NewHeightBasedScoringStrategy(valSet, currentHeight)
}
