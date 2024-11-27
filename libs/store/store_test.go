package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInMemStore(t *testing.T) {
	type pair struct {
		key string
		val int
	}
	store := NewInMemStore[string, int]()
	require.True(t, store.IsZero())
	pairs := []pair{{"key1", 1}, {"key2", 2}, {"key3", 3}}
	for _, p := range pairs {
		store.Put(p.key, p.val)
	}
	for _, p := range pairs {
		val, ok := store.Get(p.key)
		require.True(t, ok)
		require.Equal(t, p.val, val)
	}
	require.Equal(t, 3, store.Len())
	require.False(t, store.IsZero())
	require.Equal(t, []int{1, 2, 3}, store.All())
	// Query test
	{
		vals := store.Query(func(_ string, val int) bool {
			return val > 1
		}, 0)
		require.Equal(t, []int{2, 3}, vals)
	}
	// GetAndDelete test
	{
		val, ok := store.GetAndDelete(pairs[0].key)
		require.True(t, ok)
		require.Equal(t, pairs[0].val, val)
		_, ok = store.Get(pairs[0].key)
		require.False(t, ok)
		_, ok = store.GetAndDelete(pairs[0].key)
		require.False(t, ok)
	}
	// Delete test
	{
		store.Delete(pairs[1].key)
		_, ok := store.Get(pairs[1].key)
		require.False(t, ok)
	}
	// Update test
	{
		updateFun := func(_ string, it *int) {
			*it++
		}
		store.Update(pairs[2].key, updateFun)
		val, ok := store.Get(pairs[2].key)
		require.True(t, ok)
		require.Equal(t, pairs[2].val+1, val)
	}
}
