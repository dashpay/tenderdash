package node

import (
	"context"
	"fmt"
	"testing"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/evidence"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/store"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

type fakeHandoffConsensus struct{ stateSyncing, blockSyncing float64 }

func (f *fakeHandoffConsensus) SetStateSyncingMetrics(v float64) { f.stateSyncing = v }
func (f *fakeHandoffConsensus) SetBlockSyncingMetrics(v float64) { f.blockSyncing = v }

type fakeHandoffBlockSync struct{ switched []sm.State }

func (f *fakeHandoffBlockSync) SwitchToBlockSync(_ context.Context, state sm.State) error {
	f.switched = append(f.switched, state)
	return nil
}

// TestPostStateSyncHook runs the node's state sync handoff both after a
// restored snapshot and on the no-snapshot fallback, which hands over the
// unchanged local state the evidence pool was started with.
func TestPostStateSyncHook(t *testing.T) {
	testCases := []struct {
		name         string
		localHeight  int64
		handoffState int64
	}{
		{name: "no snapshots on fresh node", localHeight: 0, handoffState: 0},
		{name: "no snapshots on node with local state", localHeight: 5, handoffState: 5},
		{name: "restored snapshot", localHeight: 0, handoffState: 7},
	}
	for _, maxBytes := range []int64{1000, 0} {
		for _, tc := range testCases {
			t.Run(fmt.Sprintf("%s/evidence_max_bytes_%d", tc.name, maxBytes), func(t *testing.T) {
				local := sm.State{
					LastBlockHeight: tc.localHeight,
					Validators:      types.NewEmptyValidatorSet(),
					ConsensusParams: *types.DefaultConsensusParams(),
				}
				local.ConsensusParams.Evidence.MaxBytes = maxBytes
				pool := evidence.NewPool(
					log.NewNopLogger(),
					dbm.NewMemDB(),
					sm.NewStore(dbm.NewMemDB()),
					store.NewBlockStore(dbm.NewMemDB()),
					evidence.NopMetrics(),
					nil,
				)
				require.NoError(t, pool.Start(local))
				t.Cleanup(func() { require.NoError(t, pool.Close()) })

				handoff := local
				handoff.LastBlockHeight = tc.handoffState
				cs := &fakeHandoffConsensus{stateSyncing: 1}
				bc := &fakeHandoffBlockSync{}
				hook := newPostStateSyncHook(log.NewNopLogger(), pool, cs, bc)

				require.NotPanics(t, func() {
					require.NoError(t, hook(t.Context(), handoff))
				})
				require.Equal(t, tc.handoffState, pool.State().LastBlockHeight)
				require.Equal(t, []sm.State{handoff}, bc.switched)
				require.Zero(t, cs.stateSyncing)
				require.Equal(t, float64(1), cs.blockSyncing)
			})
		}
	}
}
