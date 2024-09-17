package validatorscoring

import (
	"fmt"
	"sync"

	"github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

type heightBasedScoringStrategy struct {
	valSet *types.ValidatorSet
	height int64
	bs     BlockCommitStore
	logger log.Logger
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

	logger, err := log.NewDefaultLogger(log.LogFormatPlain, log.LogLevelDebug) // TODO: make configurable
	if err != nil {
		return nil, fmt.Errorf("could not create logger: %w", err)
	}

	s := &heightBasedScoringStrategy{
		valSet: vset,
		height: currentHeight,
		bs:     bs,
		logger: logger,
	}

	// if we have a block store, we can determine the proposer for the current height;
	// otherwise we just trust the state of `vset`
	if bs != nil && bs.Base() > 0 && currentHeight >= bs.Base() {
		if err := selectRound0Proposer(currentHeight, s.valSet, s.bs, s.logger); err != nil {
			return nil, fmt.Errorf("could not initialize proposer: %w", err)
		}
	}
	return s, nil
}

// selectRound0Proposer determines the proposer for the given height and round 0 based on validators read from the block
// store `bs`.
// It updates the proposer in the validator set `valSet`.
func selectRound0Proposer(height int64, valSet *types.ValidatorSet, bs BlockCommitStore, logger log.Logger) error {
	if bs == nil {
		return fmt.Errorf("block store is nil")
	}

	// special case for genesis
	if height == 0 || height == 1 {
		// we take first proposer from the validator set
		if err := valSet.SetProposer(valSet.Validators[0].ProTxHash); err != nil {
			return fmt.Errorf("could not determine proposer: %w", err)
		}

		return nil
	}

	meta := bs.LoadBlockMeta(height)
	var proposer bytes.HexBytes

	addToIdx := int32(0)
	if meta == nil {
		// block not found; we try previous height, and will just add 1 to proposer index
		meta = bs.LoadBlockMeta(height - 1)
		if meta == nil {
			return fmt.Errorf("could not find block meta for previous height %d", height-1)
		}
		addToIdx = 1
	}

	if !meta.Header.ValidatorsHash.Equal(valSet.Hash()) {
		logger.Debug("quorum rotation happened",
			"height", height,
			"validators_hash", meta.Header.ValidatorsHash, "quorum_hash", valSet.QuorumHash,
			"validators", valSet,
			"meta", meta)
		// quorum rotation happened - we select 1st validator as proposer, and don't rotate
		// NOTE: We use index 1 due to bug in original code - we need to preserve the original bad behavior
		// to avoid breaking consensus
		proposer = valSet.GetByIndex(1).ProTxHash

		addToIdx = 0
	} else {
		proposer = meta.Header.ProposerProTxHash
	}

	if err := valSet.SetProposer(proposer); err != nil {
		return fmt.Errorf("could not set proposer: %w", err)
	}

	if (addToIdx + meta.Round) > 0 {
		// we want to return proposer at round 0, so we move back
		valSet.IncProposerIndex(addToIdx - meta.Round)
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
		if s.bs == nil || s.bs.Base() > s.height {
			return fmt.Errorf("cannot jump more than one height without data in block store: %d -> %d", s.height, newHeight)
		}
		// FIXME: we assume that no consensus version update happened in the meantime

		if err := selectRound0Proposer(newHeight, s.valSet, s.bs, s.logger); err != nil {
			return fmt.Errorf("could not determine proposer: %w", err)
		}

		s.height = newHeight
		return nil
	}

	s.valSet.IncProposerIndex(1)

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
		bs:     s.bs,
		logger: s.logger,
	}
}
