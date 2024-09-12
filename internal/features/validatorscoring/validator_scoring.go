package validatorscoring

import (
	"fmt"

	tmtypes "github.com/dashpay/tenderdash/proto/tendermint/types"
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

// NewProposerStrategy creates a ValidatorScoringStrategy from the ValidatorSet.
//
// Original ValidatorSet should not be used anymore. Height and round should point to the height and round that
// is reflected in validator scores, eg. the one for which GetProposer() returns proposer that generates proposal
// at the given height and round.
//
// ## Arguments
//
// - `cp` - ConsensusParams that determine scoring strategy to use
// - `valSet` - validator set to use
// - `valsetHeight` - current height of the validator set
// - `valsetRound` - current round of the validator set
// - `bs` - block store used to retreve info about historical commits; optional, can be nil
func NewProposerStrategy(cp types.ConsensusParams, valSet *types.ValidatorSet, valsetHeight int64, valsetRound int32,
	bs BlockCommitStore) (ValidatorScoringStrategy, error) {
	switch cp.Version.ConsensusVersion {
	case int32(tmtypes.VersionParams_CONSENSUS_VERSION_0):
		return NewHeightBasedScoringStrategy(valSet, valsetHeight, bs)
	case int32(tmtypes.VersionParams_CONSENSUS_VERSION_1):
		return NewHeightRoundBasedScoringStrategy(valSet, valsetHeight, valsetRound, bs)
	default:
		return nil, fmt.Errorf("unknown consensus version: %v", cp.Version.ConsensusVersion)
	}
}
