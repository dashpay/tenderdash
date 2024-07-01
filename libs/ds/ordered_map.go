package ds

import "sync"

// OrderedMap is a map with a deterministic iteration order
// this datastructure is thread-safe
type OrderedMap[T comparable, V any] struct {
	len    int
	keys   map[T]int
	values []V
	mtx    sync.RWMutex
}

// NewOrderedMap returns a new OrderedMap
func NewOrderedMap[T comparable, V any]() *OrderedMap[T, V] {
	return &OrderedMap[T, V]{
		keys: make(map[T]int),
	}
}

// Put adds a key-value pair to the map
func (m *OrderedMap[T, V]) Put(key T, val V) {
	m.mtx.Lock()
	defer m.mtx.Unlock()

	i, ok := m.keys[key]
	if ok {
		m.values[i] = val
		return
	}
	m.keys[key] = m.len
	if len(m.values) == m.len {
		m.values = append(m.values, val)
	} else {
		m.values[m.len] = val
	}
	m.len++
}

// Get returns the value for a given key
func (m *OrderedMap[T, V]) Get(key T) (V, bool) {
	m.mtx.RLock()
	defer m.mtx.RUnlock()

	i, ok := m.keys[key]
	if !ok {
		var v V
		return v, false
	}
	return m.values[i], true
}

// Has returns true if the map contains the given key
func (m *OrderedMap[T, V]) Has(key T) bool {
	m.mtx.RLock()
	defer m.mtx.RUnlock()

	_, ok := m.keys[key]
	return ok
}

// Delete removes a key-value pair from the map
func (m *OrderedMap[T, V]) Delete(key T) {
	m.mtx.Lock()
	defer m.mtx.Unlock()

	i, ok := m.keys[key]
	if !ok {
		return
	}
	delete(m.keys, key)

	m.values = append(m.values[:i], m.values[i+1:]...)
	m.len--
	for k, v := range m.keys {
		if v > i {
			m.keys[k] = v - 1
		}
	}
}

// Values returns all values in the map
func (m *OrderedMap[T, V]) Values() []V {
	m.mtx.RLock()
	defer m.mtx.RUnlock()

	return append([]V{}, m.values[0:m.len]...)
}

// Keys returns all keys in the map
func (m *OrderedMap[T, V]) Keys() []T {
	m.mtx.RLock()
	defer m.mtx.RUnlock()

	keys := make([]T, len(m.keys))
	for k, v := range m.keys {
		keys[v] = k
	}
	return keys
}

// Len returns a number of the map
func (m *OrderedMap[T, V]) Len() int {
	m.mtx.RLock()
	defer m.mtx.RUnlock()

	return m.len
}
