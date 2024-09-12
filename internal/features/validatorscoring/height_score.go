package validatorscoring

import (
	"fmt"

	"github.com/dashpay/tenderdash/types"
)

type heightBasedScoringStrategy struct {
	inner *heightRoundBasedScoringStrategy
}

// NewHeightBasedScoringStrategy creates a new height-based scoring strategy
//
// The strategy increments the ProposerPriority of each validator by 1 at each height
// and updates the proposer accordingly.
//
// Subsequent rounds at the same height will select next proposer on the list, but not persist these changes,
// so that the proposer of height H and round 1 is selected again at height H+1 and round 0.
//
// It modifies `valSet` in place.
//
// ## Arguments
//
// * `vset` - the validator set; it must not be empty and can be modified in place
// * `currentHeight` - the current height for which vset has correct scores
func NewHeightBasedScoringStrategy(vset *types.ValidatorSet, currentHeight int64, bs BlockCommitStore) (ValidatorScoringStrategy, error) {
	if vset.IsNilOrEmpty() {
		return nil, fmt.Errorf("empty validator set")
	}

	heightRoundStrategy, err := NewHeightRoundBasedScoringStrategy(vset, currentHeight, 0, bs)
	if err != nil {
		return nil, fmt.Errorf("error creating inner scoring strategy: %v", err)
	}
	s, ok := heightRoundStrategy.(*heightRoundBasedScoringStrategy)
	if !ok {
		return nil, fmt.Errorf("inner scoring strategy is not of type heightRoundBasedScoringStrategy")
	}
	return &heightBasedScoringStrategy{
		inner: s,
	}, nil
}

func (s *heightBasedScoringStrategy) UpdateScores(newHeight int64, _round int32) error {
	heightDiff := newHeight - s.inner.height
	if heightDiff == 0 {
		// NOOP
		return nil
	}
	if heightDiff < 0 {
		// TODO: handle going back in height
		return fmt.Errorf("cannot go back in height: %d -> %d", s.inner.height, newHeight)
	}

	for h := s.inner.height; h < newHeight; h++ {
		s.inner.incrementProposerPriority()
	}
	s.inner.valSet.Recalculate()
	s.inner.height = newHeight

	return nil
}

func (s *heightBasedScoringStrategy) GetProposer(height int64, round int32) (*types.Validator, error) {
	if err := s.UpdateScores(height, 0); err != nil {
		return nil, err
	}
	if round == 0 {
		return s.inner.GetProposer(height, round)
	}

	// advance a copy of the validator set to the correct round, but don't persist the changes
	inner := s.inner.Copy().(*heightRoundBasedScoringStrategy)
	proposer, err := inner.GetProposer(height, round)
	if err != nil {
		return nil, fmt.Errorf("error getting proposer for height %d and round %d: %v", height, round, err)
	}

	// now, we take proposer from original set, in case someone wants to modify it (eg. for testing)
	protx := proposer.ProTxHash
	_, proposer = s.inner.ValidatorSet().GetByProTxHash(protx)
	if proposer == nil {
		return nil, fmt.Errorf("proposer %x not found in the validator set", protx)
	}

	return proposer, nil
}

func (s *heightBasedScoringStrategy) MustGetProposer(height int64, round int32) *types.Validator {
	proposer, err := s.GetProposer(height, round)
	if err != nil {
		panic(err)
	}
	return proposer
}

func (s *heightBasedScoringStrategy) ValidatorSet() *types.ValidatorSet {
	return s.inner.ValidatorSet()
}

func (s *heightBasedScoringStrategy) Copy() ValidatorScoringStrategy {
	innerCopy := s.inner.Copy().(*heightRoundBasedScoringStrategy)
	return &heightBasedScoringStrategy{
		inner: innerCopy,
	}
}
