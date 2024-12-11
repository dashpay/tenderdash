package selectproposer

import (
	"fmt"
	"sync"
	"testing"

	"github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

type heightProposerSelector struct {
	valSet *types.ValidatorSet
	height int64
	bs     BlockStore
	logger log.Logger
	mtx    sync.Mutex
}

// NewHeightProposerSelector creates a new height-based proposer selector.
//
// This selector goes through validators in a round-robin approach, increasing proposer index by 1 at each height.
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
// * `bs` - block store used to retrieve info about historical commits
// * `logger` - logger to use
func NewHeightProposerSelector(vset *types.ValidatorSet, currentHeight int64, bs BlockStore, logger log.Logger) (ProposerSelector, error) {
	if vset.IsNilOrEmpty() {
		return nil, fmt.Errorf("empty validator set")
	}
	if logger == nil {
		logger = log.NewNopLogger()
	}

	logger.Debug("new height proposer selector", "height", currentHeight)

	s := &heightProposerSelector{
		valSet: vset,
		height: currentHeight,
		bs:     bs,
		logger: logger,
	}

	// if we have a block store, we can determine the proposer for the current height;
	// otherwise we just trust the state of `vset`
	if bs != nil && bs.Base() > 0 && currentHeight >= bs.Base() {
		if err := s.proposerFromStore(currentHeight); err != nil {
			return nil, fmt.Errorf("could not initialize proposer: %w", err)
		}
	}
	return s, nil
}

// proposerFromStore determines the proposer for the given height and round 0
// based on current or previous block stored in the block store.
func (s *heightProposerSelector) proposerFromStore(height int64) error {
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
	indexIncrement := int64(0)

	meta := s.bs.LoadBlockMeta(height)
	if meta != nil {
		// block already saved to store, just take the proposer
		if !meta.Header.ValidatorsHash.Equal(s.valSet.Hash()) {
			// we loaded the same block, so quorum should be the same
			s.logger.Error("quorum rotation detected but not expected",
				"height", height,
				"validators_hash", meta.Header.ValidatorsHash, "quorum_hash", s.valSet.QuorumHash,
				"validators", s.valSet)

			return fmt.Errorf("validator set hash mismatch at height %d", height)
		}

		proposer = meta.Header.ProposerProTxHash
		// rewind rounds, as the proposer in header is for round `meta.Round` and we want round 0
		indexIncrement = int64(-meta.Round)
	} else {
		// block not found; we try previous height, and will just add 1 to proposer index
		meta = s.bs.LoadBlockMeta(height - 1)
		if meta == nil {
			return fmt.Errorf("could not find block meta for previous height %d", height-1)
		}

		// we are at previous height, so we need to increment proposer index by 1 to go to next height
		indexIncrement = 1

		if meta.Header.ValidatorsHash.Equal(s.valSet.Hash()) {
			// validators hash matches, so we can take proposer from previous height
			proposer = meta.Header.ProposerProTxHash
			// rewind rounds, as this is how heightBasedScoringStrategy works
			indexIncrement = indexIncrement - int64(meta.Round)
		} else {
			// quorum rotation happened - we select 1st validator as proposer, and don't rotate
			// NOTE: We use index 1 due to bug in original code that causes first validator to never propose.
			// We need to preserve the original bad behavior to avoid breaking consensus
			proposer = s.valSet.GetByIndex(1).ProTxHash
			indexIncrement = 0

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
		s.valSet.IncProposerIndex(indexIncrement)
	}

	return nil
}

// UpdateHeightRound updates the scores of the validators to the given height.
// Here, we ignore the round, as we don't want to persist round info.
func (s *heightProposerSelector) UpdateHeightRound(newHeight int64, round int32) error {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	return s.updateScores(newHeight, round)
}

func (s *heightProposerSelector) updateScores(newHeight int64, _round int32) error {
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
		if s.bs == nil || s.bs.Base() > s.height {
			return fmt.Errorf("cannot jump more than one height without data in block store: %d -> %d", s.height, newHeight)
		}
		// FIXME: we assume that no consensus version update happened in the meantime

		if err := s.proposerFromStore(newHeight); err != nil {
			return fmt.Errorf("could not determine proposer: %w", err)
		}

		s.height = newHeight
		return nil
	}

	s.valSet.IncProposerIndex(1)

	s.height = newHeight

	return nil
}

func (s *heightProposerSelector) GetProposer(height int64, round int32) (*types.Validator, error) {
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
	vs.IncProposerIndex(int64(round))
	return vs.Proposer(), nil
}

func (s *heightProposerSelector) MustGetProposer(height int64, round int32) *types.Validator {
	if !testing.Testing() {
		panic("MustGetProposer should only be used in tests")
	}

	proposer, err := s.GetProposer(height, round)
	if err != nil {
		panic(err)
	}
	return proposer
}

func (s *heightProposerSelector) ValidatorSet() *types.ValidatorSet {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	return s.valSet
}

func (s *heightProposerSelector) Copy() ProposerSelector {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	return &heightProposerSelector{
		valSet: s.valSet.Copy(),
		height: s.height,
		bs:     s.bs,
		logger: s.logger,
	}
}
