package evidence_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/evidence"
	"github.com/dashpay/tenderdash/internal/evidence/mocks"
	smmocks "github.com/dashpay/tenderdash/internal/state/mocks"
	dbm "github.com/dashpay/tenderdash/libs/db"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

func TestPoolRuntimeEvidenceCycles(t *testing.T) {
	ctx := t.Context()
	vals, priv := types.RandValidatorSet(1)
	state := createState(2, vals)
	state.ConsensusParams.Evidence.MaxBytes = 0
	blocks := &mocks.BlockStore{}
	blocks.On("Base").Return(int64(1))
	blocks.On("Height").Return(int64(100))
	blocks.On("LoadBlockMeta", mock.Anything).
		Return(&types.BlockMeta{Header: types.Header{Time: defaultEvidenceTime}})
	states := &smmocks.Store{}
	states.On("LoadValidators", mock.Anything, mock.Anything).Return(vals, nil)
	db := dbm.NewMemDB()
	pool := evidence.NewPool(log.NewNopLogger(), db, states, blocks, evidence.NopMetrics(), nil)
	require.NoError(t, pool.Start(state))
	ev, err := types.NewMockDuplicateVoteEvidenceWithValidator(
		ctx,
		1,
		defaultEvidenceTime,
		priv[0],
		evidenceChainID,
		vals.QuorumType,
		vals.QuorumHash,
	)
	require.NoError(t, err)
	require.ErrorIs(t, pool.AddEvidence(ctx, ev), types.ErrEvidenceDisabled)
	require.NoError(t, pool.CheckEvidence(ctx, types.EvidenceList{ev}), "local admission mode must not reject verified block evidence")
	iterator, err := db.Iterator(nil, nil)
	require.NoError(t, err)
	require.False(t, iterator.Valid(), "disabled validation must not persist pending evidence")
	require.NoError(t, iterator.Close())
	for cycle := 0; cycle < 2; cycle++ {
		state.LastBlockHeight++
		state.ConsensusParams.Evidence.MaxBytes = 1000
		pool.Update(ctx, state, nil)
		require.NoError(t, pool.AddEvidence(ctx, ev))
		pending, _ := pool.PendingEvidence(1000)
		require.Len(t, pending, 1)
		state.LastBlockHeight++
		state.ConsensusParams.Evidence.MaxBytes = 0
		pool.Update(ctx, state, nil)
		require.ErrorIs(t, pool.AddEvidence(ctx, ev), types.ErrEvidenceDisabled)
		pending, size := pool.PendingEvidence(-1)
		require.Empty(t, pending)
		require.Zero(t, size)
	}
	// The disabling block still commits evidence under the old positive budget.
	state.LastBlockHeight++
	state.ConsensusParams.Evidence.MaxBytes = 1000
	pool.Update(ctx, state, nil)
	require.NoError(t, pool.CheckEvidence(ctx, types.EvidenceList{ev}))
	state.LastBlockHeight++
	state.ConsensusParams.Evidence.MaxBytes = 0
	pool.Update(ctx, state, types.EvidenceList{ev})
	state.LastBlockHeight++
	state.ConsensusParams.Evidence.MaxBytes = 1000
	pool.Update(ctx, state, nil)
	require.ErrorContains(t, pool.CheckEvidence(ctx, types.EvidenceList{ev}), "already committed")
	pending, _ := pool.PendingEvidence(1000)
	require.Empty(t, pending)
}

func TestPoolDisabledDoesNotTouchStorage(t *testing.T) {
	vals, _ := types.RandValidatorSet(1)
	state := createState(2, vals)
	state.ConsensusParams.Evidence.MaxBytes = 0
	// Nil stores make any accidental disabled storage access fail immediately.
	pool := evidence.NewPool(log.NewNopLogger(), nil, nil, nil, evidence.NopMetrics(), nil)
	require.NoError(t, pool.Start(state))
	require.ErrorIs(t, pool.AddEvidence(t.Context(), nil), types.ErrEvidenceDisabled)
	pool.ReportConflictingVotes(nil, nil)
	require.NoError(t, pool.CheckEvidence(t.Context(), nil))
	pending, size := pool.PendingEvidence(-1)
	require.Empty(t, pending)
	require.Zero(t, size)
	state.LastBlockHeight++
	state.LastBlockTime = state.LastBlockTime.Add(time.Second)
	pool.Update(t.Context(), state, nil)
}

func TestPoolRuntimeRestartAndExpiry(t *testing.T) {
	ctx := t.Context()
	vals, priv := types.RandValidatorSet(1)
	state := createState(2, vals)
	blocks := &mocks.BlockStore{}
	blocks.On("Base").Return(int64(1))
	blocks.On("Height").Return(int64(100))
	blocks.On("LoadBlockMeta", mock.Anything).
		Return(&types.BlockMeta{Header: types.Header{Time: defaultEvidenceTime}})
	states := &smmocks.Store{}
	states.On("LoadValidators", mock.Anything, mock.Anything).Return(vals, nil)
	dir := t.TempDir()
	openPool := func() *evidence.Pool {
		db, err := dbm.NewDB("evidence", dbm.GoLevelDBBackend, dir)
		require.NoError(t, err)
		pool := evidence.NewPool(log.NewNopLogger(), db, states, blocks, evidence.NopMetrics(), nil)
		require.NoError(t, pool.Start(state))
		return pool
	}
	pool := openPool()
	ev, err := types.NewMockDuplicateVoteEvidenceWithValidator(
		ctx,
		1,
		defaultEvidenceTime,
		priv[0],
		evidenceChainID,
		vals.QuorumType,
		vals.QuorumHash,
	)
	require.NoError(t, err)
	require.NoError(t, pool.AddEvidence(ctx, ev))
	state.LastBlockHeight++
	state.ConsensusParams.Evidence.MaxBytes = 0
	pool.Update(ctx, state, nil)
	require.NoError(t, pool.Close())
	pool = openPool()
	pending, _ := pool.PendingEvidence(-1)
	require.Empty(t, pending)
	state.LastBlockHeight++
	state.ConsensusParams.Evidence.MaxBytes = 1000
	pool.Update(ctx, state, nil)
	pending, _ = pool.PendingEvidence(1000)
	require.Len(t, pending, 1, "unexpired evidence survives a disabled restart")
	second, err := types.NewMockDuplicateVoteEvidenceWithValidator(
		ctx,
		2,
		defaultEvidenceTime,
		priv[0],
		evidenceChainID,
		vals.QuorumType,
		vals.QuorumHash,
	)
	require.NoError(t, err)
	require.NoError(t, pool.AddEvidence(ctx, second))
	state.LastBlockHeight++
	state.ConsensusParams.Evidence.MaxBytes = 0
	pool.Update(ctx, state, types.EvidenceList{ev})
	require.NoError(t, pool.Close())
	// Disabled restarts need not scan or prune the retained evidence.
	state.LastBlockHeight += state.ConsensusParams.Evidence.MaxAgeNumBlocks + 1
	state.LastBlockTime = state.LastBlockTime.Add(
		state.ConsensusParams.Evidence.MaxAgeDuration + time.Second,
	)
	pool = openPool()
	state.LastBlockHeight++
	state.ConsensusParams.Evidence.MaxBytes = 1000
	pool.Update(ctx, state, nil)
	pending, _ = pool.PendingEvidence(1000)
	require.Empty(t, pending, "reactivation prunes evidence that expired while disabled")
	require.ErrorContains(t, pool.CheckEvidence(ctx, types.EvidenceList{ev}), "already committed")
	require.NoError(t, pool.Close())
	require.NoError(t, pool.Close())
}

// TestPoolRuntimeFlushesConflictingVotesBeforeDisable reports an equivocation
// during the last enabled height and checks it survives disabling, a disabled
// restart and reactivation, while conflicts reported once disabled are dropped.
func TestPoolRuntimeFlushesConflictingVotesBeforeDisable(t *testing.T) {
	ctx := t.Context()
	vals, priv := types.RandValidatorSet(1)
	state := createState(2, vals)
	blocks := &mocks.BlockStore{}
	blocks.On("Base").Return(int64(1))
	blocks.On("Height").Return(int64(100))
	blocks.On("LoadBlockMeta", mock.Anything).
		Return(&types.BlockMeta{Header: types.Header{Time: defaultEvidenceTime}})
	states := &smmocks.Store{}
	states.On("LoadValidators", mock.Anything, mock.Anything).Return(vals, nil)
	dir := t.TempDir()
	openPool := func() *evidence.Pool {
		db, err := dbm.NewDB("evidence", dbm.GoLevelDBBackend, dir)
		require.NoError(t, err)
		pool := evidence.NewPool(log.NewNopLogger(), db, states, blocks, evidence.NopMetrics(), nil)
		require.NoError(t, pool.Start(state))
		return pool
	}
	newConflict := func(height int64) *types.DuplicateVoteEvidence {
		ev, err := types.NewMockDuplicateVoteEvidenceWithValidator(
			ctx,
			height,
			defaultEvidenceTime,
			priv[0],
			evidenceChainID,
			vals.QuorumType,
			vals.QuorumHash,
		)
		require.NoError(t, err)
		return ev
	}

	pool := openPool()
	// Height 3 is the last enabled height; its commit disables evidence.
	ev := newConflict(3)
	pool.ReportConflictingVotes(ev.VoteA, ev.VoteB)
	state.LastBlockHeight = 3
	state.LastBlockTime = ev.Time()
	state.LastValidators = vals
	state.ConsensusParams.Evidence.MaxBytes = 0
	pool.Update(ctx, state, nil)
	require.NoError(t, pool.Close())

	pool = openPool()
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	// Height 4 runs disabled; a conflict reported now must not be buffered.
	late := newConflict(4)
	pool.ReportConflictingVotes(late.VoteA, late.VoteB)
	state.LastBlockHeight = 4
	state.ConsensusParams.Evidence.MaxBytes = 1000
	pool.Update(ctx, state, nil)
	pending, _ := pool.PendingEvidence(-1)
	require.Len(t, pending, 1, "only the conflict from the last enabled height may be pending")
	require.Equal(t, ev.Hash(), pending[0].Hash())
}

func TestPoolRuntimeConcurrentTransitionsAndClose(t *testing.T) {
	vals, priv := types.RandValidatorSet(1)
	state := createState(2, vals)
	blocks := &mocks.BlockStore{}
	blocks.On("Base").Return(int64(1))
	blocks.On("Height").Return(int64(100))
	blocks.On("LoadBlockMeta", mock.Anything).
		Return(&types.BlockMeta{Header: types.Header{Time: defaultEvidenceTime}})
	states := &smmocks.Store{}
	states.On("LoadValidators", mock.Anything, mock.Anything).Return(vals, nil)
	pool := evidence.NewPool(
		log.NewNopLogger(),
		dbm.NewMemDB(),
		states,
		blocks,
		evidence.NopMetrics(),
		nil,
	)
	require.NoError(t, pool.Start(state))
	ev, err := types.NewMockDuplicateVoteEvidenceWithValidator(
		t.Context(),
		1,
		defaultEvidenceTime,
		priv[0],
		evidenceChainID,
		vals.QuorumType,
		vals.QuorumHash,
	)
	require.NoError(t, err)
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iteration := 0; iteration < 50; iteration++ {
				_ = pool.AddEvidence(t.Context(), ev)
				_ = pool.CheckEvidence(t.Context(), types.EvidenceList{ev})
				pool.PendingEvidence(1000)
				pool.ReportConflictingVotes(ev.VoteA, ev.VoteB)
			}
		}()
	}
	for iteration := 0; iteration < 20; iteration++ {
		state.LastBlockHeight++
		state.ConsensusParams.Evidence.MaxBytes = int64(iteration%2) * 1000
		pool.Update(t.Context(), state, nil)
	}
	require.NoError(t, pool.Close())
	wg.Wait()
	require.ErrorContains(t, pool.AddEvidence(t.Context(), ev), "closed")
	require.ErrorContains(t, pool.CheckEvidence(t.Context(), types.EvidenceList{ev}), "closed")
	pending, _ := pool.PendingEvidence(1000)
	require.Empty(t, pending)
}
