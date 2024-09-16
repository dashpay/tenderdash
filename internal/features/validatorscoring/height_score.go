package validatorscoring

import (
	"fmt"
	"sync"

	"github.com/dashpay/tenderdash/types"
)

type heightBasedScoringStrategy struct {
	valSet *types.ValidatorSet
	height int64
	mtx    sync.Mutex
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
func NewHeightBasedScoringStrategy(vset *types.ValidatorSet, currentHeight int64, bs BlockCommitStore) (ProposerProvider, error) {
	if vset.IsNilOrEmpty() {
		return nil, fmt.Errorf("empty validator set")
	}

	s := &heightBasedScoringStrategy{
		valSet: vset,
		height: currentHeight,
	}

	// if we have a block store, we can determine the proposer for the current height;
	// otherwise we just trust the state of `vset`
	if bs != nil {
		if err := s.initProposer(currentHeight, bs); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// initProposer determines the proposer for the given height using block store and consensus params.
func (s *heightBasedScoringStrategy) initProposer(height int64, bs BlockCommitStore) error {
	if bs == nil {
		return fmt.Errorf("block store is nil")
	}

	// special case for genesis
	if height == 0 || height == 1 {
		// we take first proposer from the validator set
		if err := s.valSet.SetProposer(s.valSet.Validators[0].ProTxHash); err != nil {
			return fmt.Errorf("could not set initial proposer: %w", err)
		}

		return nil
	}

	meta := bs.LoadBlockMeta(height)
	addToIdx := int32(0)
	if meta == nil {
		// we use previous height, and will just add 1 to proposer index
		meta = bs.LoadBlockMeta(height - 1)
		if meta == nil {
			return fmt.Errorf("could not find block meta for previous height %d", height-1)
		}
		addToIdx = 1
	}

	if err := s.valSet.SetProposer(meta.Header.ProposerProTxHash); err != nil {
		return fmt.Errorf("could not set proposer: %w", err)
	}

	if (addToIdx + meta.Round) > 0 {
		// we want to return proposer at round 0, so we move back
		s.valSet.IncProposerIndex(addToIdx - meta.Round)
	}

	return nil
}

// UpdateScores updates the scores of the validators to the given height.
// Here, we ignore the round, as we don't want to persist round info.
func (s *heightBasedScoringStrategy) UpdateScores(newHeight int64, round int32) error {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	return s.updateScores(newHeight, round)
}

func (s *heightBasedScoringStrategy) updateScores(newHeight int64, _round int32) error {
	heightDiff := newHeight - s.height
	if heightDiff == 0 {
		// NOOP
		return nil
	}
	if heightDiff < 0 {
		// TODO: handle going back in height
		return fmt.Errorf("cannot go back in height: %d -> %d", s.height, newHeight)
	}

	if heightDiff > 1 {
		return fmt.Errorf("cannot jump more than one height: %d -> %d", s.height, newHeight)
	}

	s.valSet.IncProposerIndex(int32(heightDiff)) //nolint:gosec

	s.height = newHeight

	return nil
}

func (s *heightBasedScoringStrategy) GetProposer(height int64, round int32) (*types.Validator, error) {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	if err := s.updateScores(height, 0); err != nil {
		return nil, err
	}
	if round == 0 {
		return s.valSet.Proposer(), nil
	}

	// advance a copy of the validator set to the correct round, but don't persist the changes
	vs := s.valSet.Copy()
	vs.IncProposerIndex(round)
	return vs.Proposer(), nil
}

func (s *heightBasedScoringStrategy) MustGetProposer(height int64, round int32) *types.Validator {
	proposer, err := s.GetProposer(height, round)
	if err != nil {
		panic(err)
	}
	return proposer
}

func (s *heightBasedScoringStrategy) ValidatorSet() *types.ValidatorSet {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	return s.valSet
}

func (s *heightBasedScoringStrategy) Copy() ProposerProvider {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	return &heightBasedScoringStrategy{
		valSet: s.valSet.Copy(),
		height: s.height,
	}
}
