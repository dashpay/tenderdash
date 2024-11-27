package sync

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dashpay/tenderdash/internal/p2p/conn"
)

func TestConcurrentSlice(t *testing.T) {
	s := NewConcurrentSlice[int](1, 2, 3)

	// Test Append
	s.Append(4)
	if s.Len() != 4 {
		t.Errorf("Expected length of slice to be 4, got %d", s.Len())
	}

	// Test Get
	if s.Get(3) != 4 {
		t.Errorf("Expected element at index 3 to be 4, got %d", s.Get(3))
	}

	// Test Set
	s.Set(1, 5)

	// Test ToSlice
	slice := s.ToSlice()
	if len(slice) != 4 || slice[3] != 4 || slice[1] != 5 {
		t.Errorf("Expected ToSlice to return [1 5 3 4], got %v", slice)
	}

	// Test Reset
	s.Reset()
	if s.Len() != 0 {
		t.Errorf("Expected length of slice to be 0 after Reset, got %d", s.Len())
	}

	// Test Copy
	s.Append(5)
	copy := s.Copy()
	if copy.Len() != 1 || copy.Get(0) != 5 {
		t.Errorf("Expected Copy to return a new slice with [5], got %v", copy.ToSlice())
	}
}

func TestConcurrentSlice_Concurrency(t *testing.T) {
	s := NewConcurrentSlice[int]()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			s.Append(val)
		}(i)
	}

	wg.Wait()

	assert.Equal(t, 100, s.Len())

	if s.Len() != 100 {
		t.Errorf("Expected length of slice to be 100, got %d", s.Len())
	}

	for i := 0; i < 100; i++ {
		assert.Contains(t, s.ToSlice(), i)
	}
}

func TestConcurrentSlice_MarshalUnmarshalJSON(t *testing.T) {
	type node struct {
		Channels *ConcurrentSlice[conn.ChannelID]
	}
	cs := NewConcurrentSlice[conn.ChannelID](1, 2, 3)

	node1 := node{
		Channels: cs,
	}

	// Marshal to JSON
	data, err := json.Marshal(node1)
	assert.NoError(t, err, "Failed to marshal concurrentSlice")

	// Unmarshal from JSON
	node2 := node{
		// Channels: NewConcurrentSlice[conn.ChannelID](),
	}

	err = json.Unmarshal(data, &node2)
	assert.NoError(t, err, "Failed to unmarshal concurrentSlice")

	assert.EqualValues(t, node1.Channels.ToSlice(), node2.Channels.ToSlice())
}
