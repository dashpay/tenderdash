package evidence

import (
	"context"

	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/types"
)

// DisabledPool handles chains whose consensus evidence byte limit is zero.
// It retains no evidence and performs no database or network operations.
type DisabledPool struct{}

func (DisabledPool) Start(state sm.State) error {
	return (types.EvidenceParams{}).ValidateUpdate(state.ConsensusParams.Evidence)
}

func (DisabledPool) AddEvidence(context.Context, types.Evidence) error {
	return types.ErrEvidenceDisabled
}

func (DisabledPool) CheckEvidence(_ctx context.Context, ev types.EvidenceList) error {
	if len(ev) != 0 {
		return types.ErrEvidenceDisabled
	}
	return nil
}

func (DisabledPool) PendingEvidence(_maxBytes int64) ([]types.Evidence, int64) {
	return nil, 0
}

func (DisabledPool) Update(context.Context, sm.State, types.EvidenceList) {}

func (DisabledPool) ReportConflictingVotes(_voteA, _voteB *types.Vote) {}
