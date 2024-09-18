package selectproposer

import (
	"fmt"

	"github.com/dashpay/tenderdash/libs/log"
	tmtypes "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

type ProposerSelector interface {
	// GetProposer returns the proposer for the given height and round. It calls Update if necessary.
	GetProposer(height int64, round int32) (*types.Validator, error)

	// MustGetProposer returns the proposer for the given height and round. It calls Update if necessary.
	// It panics on any error.
	//
	// Use in tests
	// See [GetProposer](#GetProposer) for details.
	MustGetProposer(height int64, round int32) *types.Validator
	// UpdateHeightRound updates proposer to match provided height and round. It should be called at least once for each round.
	//   - `height` is the height
	//  - `round` is the round
	UpdateHeightRound(height int64, round int32) error
	// Returns pointer to underlying validator set; not thread-safe, and should be modified with caution
	ValidatorSet() *types.ValidatorSet
	// Create deep copy of the strategy and its underlying validator set
	Copy() ProposerSelector
}

type BlockStore interface {
	LoadBlockCommit(height int64) *types.Commit
	LoadBlockMeta(height int64) *types.BlockMeta
	Base() int64
}

// NewProposerSelector creates an instance of ProposerSelector based on the given ConsensusParams.
//
// Original ValidatorSet should not be used anymore. Height and round should point to the height and round that
// is reflected in validator scores, eg. the one for which GetProposer() returns proposer that generates proposal
// at the given height and round.
//
// If block store is provided, it will be used to determine the proposer for the current height.
//
// ## Arguments
//
// - `cp` - ConsensusParams that determine scoring strategy to use
// - `valSet` - validator set to use
// - `valsetHeight` - current height of the validator set
// - `valsetRound` - current round of the validator set
// - `bs` - block store used to retreve info about historical commits
// - `logger` - logger to use; can be nil
func NewProposerSelector(cp types.ConsensusParams, valSet *types.ValidatorSet, valsetHeight int64, valsetRound int32,
	bs BlockStore, logger log.Logger) (ProposerSelector, error) {
	if logger == nil {
		logger = log.NewNopLogger()
	}
	switch cp.Version.ConsensusVersion {
	case int32(tmtypes.VersionParams_CONSENSUS_VERSION_0):
		return NewHeightProposerSelector(valSet, valsetHeight, bs, logger)
	case int32(tmtypes.VersionParams_CONSENSUS_VERSION_1):

		return NewHeightRoundProposerSelector(valSet, valsetHeight, valsetRound, bs, logger)
	default:
		return nil, fmt.Errorf("unknown consensus version: %v", cp.Version.ConsensusVersion)
	}
}
