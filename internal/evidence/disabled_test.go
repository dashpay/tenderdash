package evidence_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/evidence"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/types"
)

func TestDisabledPool(t *testing.T) {
	pool := evidence.DisabledPool{}
	require.NoError(t, pool.Start(sm.State{}))
	require.Error(t, pool.Start(sm.State{ConsensusParams: *types.DefaultConsensusParams()}))
	require.NoError(t, pool.CheckEvidence(t.Context(), nil))
	require.ErrorIs(
		t,
		pool.CheckEvidence(t.Context(), types.EvidenceList{nil}),
		types.ErrEvidenceDisabled,
	)
	var workers sync.WaitGroup
	for i := 0; i < 10; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				// A disabled pool must not inspect, retain, or process votes or evidence.
				pool.ReportConflictingVotes(nil, nil)
				pool.Update(t.Context(), sm.State{}, types.EvidenceList{nil})
				assert.ErrorIs(t, pool.AddEvidence(t.Context(), nil), types.ErrEvidenceDisabled)
				ev, size := pool.PendingEvidence(-1)
				assert.Empty(t, ev)
				assert.Zero(t, size)
			}
		}()
	}
	workers.Wait()
}
