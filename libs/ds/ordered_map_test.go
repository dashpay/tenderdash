package ds

import (
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrderedMap(t *testing.T) {
	om := NewOrderedMap[string, int]()
	require.Equal(t, 0, om.Len())
	_, ok := om.Get("a")
	require.False(t, ok)
	require.False(t, om.Has("a"))
	om.Put("a", 1)
	require.True(t, om.Has("a"))
	require.Equal(t, 1, om.Len())
	val, ok := om.Get("a")
	require.Equal(t, 1, val)
	require.True(t, ok)
	require.Equal(t, 1, om.Len())
	om.Put("a", 2)
	val, ok = om.Get("a")
	require.Equal(t, 2, val)
	require.True(t, ok)
	require.Equal(t, 1, om.Len())
	om.Put("b", 3)
	val, ok = om.Get("b")
	require.Equal(t, 3, val)
	require.True(t, ok)
	require.Equal(t, 2, om.Len())

	require.Equal(t, []int{2, 3}, om.Values())
	require.Equal(t, []string{"a", "b"}, om.Keys())

	om.Delete("b")
	require.Equal(t, []int{2}, om.Values())
	require.Equal(t, []string{"a"}, om.Keys())

	// delete unknown key
	om.Delete("c")
}

// / Run TestOrderedMap in parallel
func TestOrderedMapMultithread(t *testing.T) {
	threads := 100

	wg := sync.WaitGroup{}
	wg.Add(threads)

	for i := 0; i < threads; i++ {
		go func(id int) {
			t.Run(strconv.FormatInt(int64(id), 10), TestOrderedMap)
			wg.Done()
		}(i)
	}

	wg.Wait()
}

func TestOrderedMapDelete(t *testing.T) {
	m := NewOrderedMap[int, int]()
	m.Put(1, 1)
	m.Put(2, 2)
	m.Put(3, 3)
	m.Delete(2)
	keys := m.Keys()
	if len(keys) != 2 {
		t.Errorf("Expected 2 keys, got %d", len(keys))
	}
	if keys[0] != 1 && keys[1] != 3 {
		t.Errorf("Expected keys [1, 3], got %v", keys)
	}
	v1, ok := m.Get(1)
	assert.Equal(t, v1, 1)
	assert.True(t, ok)

	v3, ok := m.Get(3)
	assert.Equal(t, v3, 3)
	assert.True(t, ok)
}
