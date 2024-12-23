package selectproposer

import (
	"fmt"
	"sync"
	"testing"

	"github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

type heightRoundProposerSelector struct {
	valSet *types.ValidatorSet
	height int64
	round  int32
	bs     BlockStore
	logger log.Logger
	mtx    sync.Mutex
}

// NewHeightRoundProposerSelector creates a new proposer selector that goes around the validator set and ensures
// every proposer votes once.
//
// Each height and round increases proposer index. For example, that if a proposer is selected at height H and round R
// and the block is committed, next proposer will be selected at height H+1 and round 0 (contrary to heightProposerSelector).
//
// It modifies `valSet` in place.
//
// ## Arguments
//
// * `vset` - the validator set; it must not be empty and can be modified in place
// * `currentHeight` - the current height for which vset has correct scores
// * `currentRound` - the current round for which vset has correct scores
// * `bs` - the block store to use for historical data; can be nil
// * `logger` - the logger to use; can be nil
func NewHeightRoundProposerSelector(vset *types.ValidatorSet, currentHeight int64, currentRound int32, bs BlockStore, logger log.Logger) (ProposerSelector, error) {
	if vset.IsNilOrEmpty() {
		return nil, fmt.Errorf("empty validator set")
	}
	if logger == nil {
		logger = log.NewNopLogger()
	}

	logger.Debug("new height round proposer selector", "height", currentHeight, "round", currentRound)

	s := &heightRoundProposerSelector{
		valSet: vset,
		height: currentHeight,
		round:  currentRound,
		bs:     bs,
		logger: logger,
	}

	// if we have a block store, we can determine the proposer for the current height;
	// otherwise we just trust the state of `vset`
	if bs != nil && bs.Base() > 0 && currentHeight >= bs.Base() {
		if err := s.proposerFromStore(currentHeight, currentRound); err != nil {
			return nil, fmt.Errorf("could not initialize proposer: %w", err)
		}
	}

	return s, nil
}

// proposerFromStore determines the proposer for the given height and round using current or previous block read from
// the block store.
//
// Requires s.valSet to be set to correct validator set.
func (s *heightRoundProposerSelector) proposerFromStore(height int64, round int32) error {
	if s.bs == nil {
		return fmt.Errorf("block store is nil")
	}

	// special case for genesis
	if height == 0 || height == 1 {
		// we take first proposer from the validator set
		if err := s.valSet.SetProposer(s.valSet.Validators[0].ProTxHash); err != nil {
			return fmt.Errorf("could not determine proposer: %w", err)
		}

		return nil
	}

	var proposer bytes.HexBytes
	indexIncrement := int32(0)

	meta := s.bs.LoadBlockMeta(height)
	if meta != nil {
		valSetHash := s.valSet.Hash()
		// block already saved to store, just take the proposer
		if !meta.Header.ValidatorsHash.Equal(valSetHash) {
			// we loaded the same block, so quorum should be the same
			s.logger.Error("quorum rotation detected but not expected",
				"height", height,
				"validators_hash", meta.Header.ValidatorsHash,
				"expected_validators_hash", valSetHash,
				"next_validators_hash", meta.Header.NextValidatorsHash,
				"validators", s.valSet)

			return fmt.Errorf("validators hash mismatch at height %d", height)
		}

		proposer = meta.Header.ProposerProTxHash
		// adjust round number to match the requested one
		indexIncrement = round - meta.Round
	} else {
		// block not found; we try previous height, and will just add 1 to proposer index
		meta = s.bs.LoadBlockMeta(height - 1)
		if meta == nil {
			return fmt.Errorf("could not find block meta for previous height %d", height-1)
		}

		if meta.Header.ValidatorsHash.Equal(s.valSet.Hash()) {
			// validators hash matches, so we can take proposer from previous height
			proposer = meta.Header.ProposerProTxHash
			// we are at previous height+prev committed round, so we need to increment proposer index by 1 to go to next height,
			// and then by round number to match the requested round
			indexIncrement = 1 + round
		} else {
			// quorum rotation happened - we select 1st validator as proposer, and only adjust round number
			proposer = s.valSet.GetByIndex(0).ProTxHash
			indexIncrement = round

			s.logger.Debug("quorum rotation detected, setting proposer to 1st validator",
				"height", height,
				"validators_hash", meta.Header.ValidatorsHash, "quorum_hash", s.valSet.QuorumHash,
				"validators", s.valSet,
				"proposer_proTxHash", proposer)
		}
	}

	// we're done, set the proposer
	if err := s.valSet.SetProposer(proposer); err != nil {
		return fmt.Errorf("could not set proposer: %w", err)
	}

	if indexIncrement != 0 {
		s.valSet.IncProposerIndex(int64(indexIncrement))
	}

	return nil
}

// UpdateHeightRound updates the scores of the validators to the given height and round.
func (s *heightRoundProposerSelector) UpdateHeightRound(newHeight int64, newRound int32) error {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	return s.updateScores(newHeight, newRound)
}

func (s *heightRoundProposerSelector) updateScores(newHeight int64, newRound int32) error {
	heightDiff := newHeight - s.height
	roundDiff := int64(newRound - s.round)
	if heightDiff == 0 && roundDiff == 0 {
		// NOOP
		return nil
	}

	if heightDiff == 0 && roundDiff != 0 {
		// only update round
		s.valSet.IncProposerIndex(roundDiff)
		s.round = newRound
		return nil
	}

	if heightDiff < 0 {
		// TODO: handle going back in height
		return fmt.Errorf("cannot go back in height: %d -> %d", s.height, newHeight)
	}

	if heightDiff > 1 {
		if s.bs == nil || s.bs.Base() > s.height {
			return fmt.Errorf("cannot jump more than one height without data in block store: %d -> %d", s.height, newHeight)
		}
		// we assume that no consensus version update happened in the meantime
		if err := s.proposerFromStore(newHeight, newRound); err != nil {
			return fmt.Errorf("could not determine proposer: %w", err)
		}

		s.height = newHeight
		s.round = newRound

		return nil
	}

	// heightDiff is 1; it means we go to the newHeight+ newRound
	// Assuming s.round is the last one as we are not able to determine this
	s.valSet.IncProposerIndex(heightDiff + int64(newRound))

	s.height = newHeight
	s.round = newRound

	return nil
}

func (s *heightRoundProposerSelector) GetProposer(height int64, round int32) (*types.Validator, error) {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	if err := s.updateScores(height, round); err != nil {
		return nil, err
	}

	return s.valSet.Proposer(), nil
}

func (s *heightRoundProposerSelector) MustGetProposer(height int64, round int32) *types.Validator {
	if !testing.Testing() {
		panic("MustGetProposer should only be used in tests")
	}

	proposer, err := s.GetProposer(height, round)
	if err != nil {
		panic(err)
	}
	return proposer
}

func (s *heightRoundProposerSelector) ValidatorSet() *types.ValidatorSet {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	return s.valSet
}

func (s *heightRoundProposerSelector) Copy() ProposerSelector {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	return &heightRoundProposerSelector{
		valSet: s.valSet.Copy(),
		height: s.height,
		round:  s.round,
		bs:     s.bs,
		logger: s.logger,
	}
}
