package sync

import "sync"

type concurrentSlice[T any] struct {
	mtx   sync.RWMutex
	slice []T
}

// Slice is a thread-safe slice interface
type Slice[T any] interface {
	Append(val ...T)
	Reset()
	Get(index int) T
	Set(index int, val T)
	ToSlice() []T
	Len() int
	Copy() Slice[T]
}

// NewConcurrentSlice creates a new thread-safe slice.
// It is safe to use from multiple goroutines without additional locking.
// It can be referenced by value, and will behave similarly to a regular slice (which is a reference type).
func NewConcurrentSlice[T any](initial ...T) Slice[T] {
	return &concurrentSlice[T]{
		slice: initial,
	}
}

// Append adds an element to the slice
func (s *concurrentSlice[T]) Append(val ...T) {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	s.slice = append(s.slice, val...)
}

// Reset removes all elements from the slice
func (s *concurrentSlice[T]) Reset() {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	s.slice = []T{}
}

// Get returns the value at the given index
func (s *concurrentSlice[T]) Get(index int) T {
	s.mtx.RLock()
	defer s.mtx.RUnlock()

	return s.slice[index]
}

func (s *concurrentSlice[T]) Set(index int, val T) {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	if index > len(s.slice) {
		panic("index out of range")
	} else if index == len(s.slice) {
		s.slice = append(s.slice, val)
		return
	}

	s.slice[index] = val
}

// ToSlice returns a copy of the underlying slice
func (s *concurrentSlice[T]) ToSlice() []T {
	s.mtx.RLock()
	defer s.mtx.RUnlock()

	slice := make([]T, len(s.slice))
	copy(slice, s.slice)
	return slice
}

// Len returns the length of the slice
func (s *concurrentSlice[T]) Len() int {
	s.mtx.RLock()
	defer s.mtx.RUnlock()

	return len(s.slice)
}

// Copy returns a new deep copy of concurrentSlice with the same elements
func (s *concurrentSlice[T]) Copy() Slice[T] {
	s.mtx.RLock()
	defer s.mtx.RUnlock()

	return &concurrentSlice[T]{
		slice: s.ToSlice(),
	}
}
