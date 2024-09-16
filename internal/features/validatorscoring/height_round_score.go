package validatorscoring

import (
	"fmt"
	"sync"

	"github.com/dashpay/tenderdash/types"
)

type BlockCommitStore interface {
	LoadBlockCommit(height int64) *types.Commit
	LoadBlockMeta(height int64) *types.BlockMeta
	Base() int64
}

type heightRoundBasedScoringStrategy struct {
	valSet *types.ValidatorSet
	height int64
	round  int32

	commitStore BlockCommitStore

	mtx sync.Mutex
}

// NewheightRoundBasedScoringStrategy creates a new scoring strategy that considers both height and round.
//
// The strategy increments the ProposerPriority of each validator at each height and round
// and updates the proposer accordingly.
//
// Subsequent rounds at the same height will select next proposer on the list and persist these changes,
// so that if a proposer is selected at height H and round 1, then next proposer is selected at height H+1 and round 0.
//
// It modifies `valSet` in place.
//
// ## Arguments
//
// * `vset` - the validator set; it must not be empty and can be modified in place
// * `currentHeight` - the current height for which vset has correct scores
// * `currentRound` - the current round for which vset has correct scores
// * `commitStore` - the block store to fetch commits from; can be nil if only height changes by 1 are expected
func NewHeightRoundBasedScoringStrategy(vset *types.ValidatorSet,
	currentHeight int64, currentRound int32,
	commitStore BlockCommitStore,
) (ProposerProvider, error) {
	if vset.IsNilOrEmpty() {
		return nil, fmt.Errorf("empty validator set")
	}

	return &heightRoundBasedScoringStrategy{
		valSet:      vset,
		height:      currentHeight,
		round:       currentRound,
		commitStore: commitStore,
	}, nil
}

func (s *heightRoundBasedScoringStrategy) UpdateScores(h int64, r int32) error {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	if err := s.updateHeightRound(h, r); err != nil {
		return fmt.Errorf("update validator set for height: %w", err)
	}

	return nil
}

// updateHeightRound updates the validator scores from the current height to the newHeight, round 0.
func (s *heightRoundBasedScoringStrategy) updateHeightRound(newHeight int64, newRound int32) error {
	heightDiff := newHeight - s.height
	if heightDiff == 0 {
		return s.updateRound(newRound)
	}
	if heightDiff < 0 {
		// TODO: handle going back in height
		return fmt.Errorf("cannot go back in height: %d -> %d", s.height, newHeight)
	}

	if heightDiff == 1 && newRound == 0 && s.commitStore == nil {
		// we assume it's just new round, and we have valset for the last round of previous height
		s.valSet.IncProposerIndex(1)
		return nil
	}

	if s.commitStore == nil {
		return fmt.Errorf("block store required to update validator scores from %d:%d to %d:%d", s.height, s.round, newHeight, newRound)
	}
	currentHeight := s.height
	// process all heights from current height to new height, exclusive (as the new height is not processed yet, and
	// we have target round provided anyway).
	for h := currentHeight; h < newHeight; h++ {
		// get the last commit for the height
		commit := s.commitStore.LoadBlockCommit(h)
		if commit == nil {
			return fmt.Errorf("cannot find commit for height %d", h)
		}

		// go from round 0 to commit round (h, commit.Round)
		if s.updateRound(commit.Round) != nil {
			return fmt.Errorf("cannot update validator scores to round %d:%d", h, commit.Round)
		}
		// go to (h+1, 0)
		s.valSet.IncProposerIndex(1)
		s.height = h + 1
		s.round = 0
	}

	// so we are at newheight, round 0
	return nil
}

func (s *heightRoundBasedScoringStrategy) updateRound(newRound int32) error {
	roundDiff := newRound - s.round
	if roundDiff == 0 {
		return nil
	}
	if roundDiff < 0 {
		return fmt.Errorf("cannot go back in round: %d -> %d", s.round, newRound)
	}

	s.valSet.IncProposerIndex(roundDiff)
	s.round = newRound

	return nil
}

func (s *heightRoundBasedScoringStrategy) GetProposer(height int64, round int32) (*types.Validator, error) {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	if s.valSet.IsNilOrEmpty() {
		return nil, fmt.Errorf("empty validator set")
	}

	if err := s.updateHeightRound(height, round); err != nil {
		return nil, fmt.Errorf("update validator set for height: %w", err)
	}
	val := s.valSet.Proposer()
	if val == nil {
		return nil, fmt.Errorf("no validator found at height %d, round %d", height, round)
	}
	return val, nil
}

func (s *heightRoundBasedScoringStrategy) MustGetProposer(height int64, round int32) *types.Validator {
	proposer, err := s.GetProposer(height, round)
	if err != nil {
		panic(err)
	}
	return proposer
}

// Not thread safe
func (s *heightRoundBasedScoringStrategy) ValidatorSet() *types.ValidatorSet {
	return s.valSet
}

func (s *heightRoundBasedScoringStrategy) Copy() ProposerProvider {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	return &heightRoundBasedScoringStrategy{
		valSet:      s.valSet.Copy(),
		height:      s.height,
		round:       s.round,
		commitStore: s.commitStore,
	}
}
