package statesync

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/types"
)

func TestBackfillDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reason := errors.New("unverifiable commit")
	newPool := func(peers ...types.NodeID) *peerList {
		pool := newPeerList()
		for _, peer := range peers {
			pool.Append(peer)
		}
		return pool
	}

	t.Run("quarantine evicts the peer and bars it from later dispatch", func(t *testing.T) {
		bad, good := factory.RandomNodeID(t), factory.RandomNodeID(t)
		pool := newPool(bad, good)
		dispatch := newBackfillDispatch(pool)

		dispatch.quarantine(bad, reason)

		require.False(t, pool.Contains(bad), "a quarantined peer must leave the pool at once")
		require.False(t, dispatch.acquire(bad), "a quarantined peer must never be dispatched to again")
		require.True(t, dispatch.acquire(good))
	})

	t.Run("a peer quarantined mid-fetch is not returned to the pool", func(t *testing.T) {
		bad := factory.RandomNodeID(t)
		pool := newPool(bad)
		dispatch := newBackfillDispatch(pool)

		require.Equal(t, bad, pool.Pop(ctx))
		require.True(t, dispatch.acquire(bad))
		dispatch.quarantine(bad, reason)
		dispatch.release(bad, true)

		require.False(t, pool.Contains(bad), "the fetch that was holding it must not re-admit it")
	})

	t.Run("a run is stalled only once no peer is left anywhere", func(t *testing.T) {
		bad, good := factory.RandomNodeID(t), factory.RandomNodeID(t)
		pool := newPool(bad, good)
		dispatch := newBackfillDispatch(pool)

		require.NoError(t, dispatch.stalled(), "no quarantine, no stall")

		dispatch.quarantine(bad, reason)
		require.NoError(t, dispatch.stalled(), "a peer is still in the pool")

		require.Equal(t, good, pool.Pop(ctx))
		require.NoError(t, dispatch.stalled(),
			"a peer handed out but not yet acquired is in use, not missing")

		require.True(t, dispatch.acquire(good))
		require.NoError(t, dispatch.stalled(), "a fetch is outstanding")

		dispatch.release(good, false)
		require.ErrorIs(t, dispatch.stalled(), reason, "nothing is left to fetch from")
	})

	t.Run("hand-outs made before the run do not mask a stall", func(t *testing.T) {
		peer := factory.RandomNodeID(t)
		pool := newPool(peer)
		// The pool outlives a backfill run: a previous one may already have taken
		// peers from it.
		require.Equal(t, peer, pool.Pop(ctx))
		pool.Append(peer)

		dispatch := newBackfillDispatch(pool)
		dispatch.quarantine(peer, reason)

		require.ErrorIs(t, dispatch.stalled(), reason,
			"a stall must be reported however many peers the pool handed out earlier")
	})
}
