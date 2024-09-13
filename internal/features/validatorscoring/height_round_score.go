package validatorscoring

import (
	"fmt"
	"math"
	"math/big"
	"sync"

	tmmath "github.com/dashpay/tenderdash/libs/math"
	"github.com/dashpay/tenderdash/types"
)

type BlockCommitStore interface {
	LoadBlockCommit(height int64) *types.Commit
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
) (ValidatorScoringStrategy, error) {
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

	// ensure all scores are correctly adjusted
	s.valSet.Recalculate()

	if err := s.updateHeight(h, r); err != nil {
		return fmt.Errorf("update validator set for height: %w", err)
	}

	return nil
}

// updateHeight updates the validator scores from the current height to the newHeight, round 0.
func (s *heightRoundBasedScoringStrategy) updateHeight(newHeight int64, newRound int32) error {
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
		s.updatePriorities(1)
		return nil
	}

	if s.commitStore == nil {
		return fmt.Errorf("block store required to update validator scores from %d:%d to %d:%d", s.height, s.round, newHeight, newRound)
	}
	current_height := s.height
	// process all heights from current height to new height, exclusive (as the new height is not processed yet, and
	// we have target round provided anyway).
	for h := current_height; h < newHeight; h++ {
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
		s.updatePriorities(1)
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

	s.updatePriorities(int64(roundDiff))
	s.round = newRound

	return nil
}

func (s *heightRoundBasedScoringStrategy) GetProposer(height int64, round int32) (*types.Validator, error) {
	// no locking here, as we rely on locks inside UpdateScores
	if err := s.UpdateScores(height, round); err != nil {
		return nil, fmt.Errorf("cannot update scores for height %d round %d: %w", height, round, err)
	}

	s.mtx.Lock()
	defer s.mtx.Unlock()
	val := s.getValWithMostPriority()
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

func (s *heightRoundBasedScoringStrategy) Copy() ValidatorScoringStrategy {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	return &heightRoundBasedScoringStrategy{
		valSet:      s.valSet.Copy(),
		height:      s.height,
		round:       s.round,
		commitStore: s.commitStore,
	}
}

// updatePriorities increments ProposerPriority of each validator and
// updates the proposer. Panics if validator set is empty.
// `times` must be positive.
func (s *heightRoundBasedScoringStrategy) updatePriorities(times int64) {
	if s.valSet.IsNilOrEmpty() {
		panic("empty validator set")
	}
	if times < 0 {
		panic("Cannot call updatePriorities with negative times")
	}

	// Cap the difference between priorities to be proportional to 2*totalPower by
	// re-normalizing priorities, i.e., rescale all priorities by multiplying with:
	//  2*totalVotingPower/(maxPriority - minPriority)
	diffMax := types.PriorityWindowSizeFactor * s.valSet.TotalVotingPower()
	s.valSet.RescalePriorities(diffMax)

	var proposer *types.Validator
	// Call IncrementProposerPriority(1) times times.
	for i := int64(0); i < times; i++ {
		proposer = s.incrementProposerPriority()
	}

	s.valSet.Proposer = proposer
}

func (s *heightRoundBasedScoringStrategy) incrementProposerPriority() *types.Validator {
	vals := s.valSet
	for _, val := range vals.Validators {
		// Check for overflow for sum.
		newPrio := tmmath.SafeAddClipInt64(val.ProposerPriority, val.VotingPower)
		val.ProposerPriority = newPrio
	}
	// Decrement the validator with most ProposerPriority.
	mostest := s.getValWithMostPriority()
	// Mind the underflow.
	mostest.ProposerPriority = tmmath.SafeSubClipInt64(mostest.ProposerPriority, vals.TotalVotingPower())

	return mostest
}

// safe addition/subtraction/multiplication

// Should not be called on an empty validator set.
func (s *heightRoundBasedScoringStrategy) computeAvgProposerPriority() int64 {
	vals := s.valSet
	n := int64(len(vals.Validators))
	sum := big.NewInt(0)
	for _, val := range vals.Validators {
		sum.Add(sum, big.NewInt(val.ProposerPriority))
	}
	avg := sum.Div(sum, big.NewInt(n))
	if avg.IsInt64() {
		return avg.Int64()
	}

	// This should never happen: each val.ProposerPriority is in bounds of int64.
	panic(fmt.Sprintf("Cannot represent avg ProposerPriority as an int64 %v", avg))
}

// Compute the difference between the max and min ProposerPriority of that set.
func computeMaxMinPriorityDiff(vals *types.ValidatorSet) int64 {
	if vals.IsNilOrEmpty() {
		panic("empty validator set")
	}
	max := int64(math.MinInt64)
	min := int64(math.MaxInt64)
	for _, v := range vals.Validators {
		if v.ProposerPriority < min {
			min = v.ProposerPriority
		}
		if v.ProposerPriority > max {
			max = v.ProposerPriority
		}
	}
	diff := max - min
	if diff < 0 {
		return -1 * diff
	}
	return diff
}

func (s *heightRoundBasedScoringStrategy) getValWithMostPriority() *types.Validator {
	vals := s.valSet
	var res *types.Validator
	for _, val := range vals.Validators {
		res = res.CompareProposerPriority(val)
	}
	return res
}
