package sync

import (
	"encoding/json"
	"fmt"
	"sync"
)

// ConcurrentSlice is a thread-safe slice.
//
// It is safe to use from multiple goroutines without additional locking.
// It should be referenced by pointer.
//
// Initialize using NewConcurrentSlice().
type ConcurrentSlice[T any] struct {
	mtx   sync.RWMutex
	items []T
}

// NewConcurrentSlice creates a new thread-safe slice.
func NewConcurrentSlice[T any](initial ...T) *ConcurrentSlice[T] {
	return &ConcurrentSlice[T]{
		items: initial,
	}
}

// Append adds an element to the slice
func (s *ConcurrentSlice[T]) Append(val ...T) {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	s.items = append(s.items, val...)
}

// Reset removes all elements from the slice
func (s *ConcurrentSlice[T]) Reset() {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	s.items = []T{}
}

// Get returns the value at the given index
func (s *ConcurrentSlice[T]) Get(index int) T {
	s.mtx.RLock()
	defer s.mtx.RUnlock()

	return s.items[index]
}

// Set updates the value at the given index.
// If the index is greater than the length of the slice, it panics.
// If the index is equal to the length of the slice, the value is appended.
// Otherwise, the value at the index is updated.
func (s *ConcurrentSlice[T]) Set(index int, val T) {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	if index > len(s.items) {
		panic("index out of range")
	} else if index == len(s.items) {
		s.items = append(s.items, val)
		return
	}

	s.items[index] = val
}

// ToSlice returns a copy of the underlying slice
func (s *ConcurrentSlice[T]) ToSlice() []T {
	s.mtx.RLock()
	defer s.mtx.RUnlock()

	slice := make([]T, len(s.items))
	copy(slice, s.items)
	return slice
}

// Len returns the length of the slice
func (s *ConcurrentSlice[T]) Len() int {
	s.mtx.RLock()
	defer s.mtx.RUnlock()

	return len(s.items)
}

// Copy returns a new deep copy of concurrentSlice with the same elements
func (s *ConcurrentSlice[T]) Copy() ConcurrentSlice[T] {
	s.mtx.RLock()
	defer s.mtx.RUnlock()

	return ConcurrentSlice[T]{
		items: s.ToSlice(),
	}
}

// MarshalJSON implements the json.Marshaler interface.
func (cs *ConcurrentSlice[T]) MarshalJSON() ([]byte, error) {
	cs.mtx.RLock()
	defer cs.mtx.RUnlock()

	return json.Marshal(cs.items)
}

// UnmarshalJSON implements the json.Unmarshaler interface.
func (cs *ConcurrentSlice[T]) UnmarshalJSON(data []byte) error {
	var items []T
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}

	cs.mtx.Lock()
	defer cs.mtx.Unlock()

	cs.items = items
	return nil
}

func (cs *ConcurrentSlice[T]) String() string {
	cs.mtx.RLock()
	defer cs.mtx.RUnlock()

	return fmt.Sprintf("%v", cs.items)
}
