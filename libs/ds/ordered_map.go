package ds

// OrderedMap is a map with a deterministic iteration order
// this datastructure is not thread-safe
type OrderedMap[T comparable, V any] struct {
	keys   map[T]int
	values []V
}

// NewOrderedMap returns a new OrderedMap
func NewOrderedMap[T comparable, V any]() *OrderedMap[T, V] {
	return &OrderedMap[T, V]{
		keys: make(map[T]int),
	}
}

// Put adds a key-value pair to the map
func (m *OrderedMap[T, V]) Put(key T, val V) {
	i, ok := m.keys[key]
	if ok {
		m.values[i] = val
		return
	}
	m.keys[key] = len(m.values)
	m.values = append(m.values, val)
}

// Get returns the value for a given key
func (m *OrderedMap[T, V]) Get(key T) (V, bool) {
	i, ok := m.keys[key]
	if !ok {
		var v V
		return v, false
	}
	return m.values[i], true
}

// Has returns true if the map contains the given key
func (m *OrderedMap[T, V]) Has(key T) bool {
	_, ok := m.keys[key]
	return ok
}

// Delete removes a key-value pair from the map
func (m *OrderedMap[T, V]) Delete(key T) {
	i, ok := m.keys[key]
	if !ok {
		return
	}
	delete(m.keys, key)
	m.values = append(m.values[:i], m.values[i+1:]...)
}

// Values returns all values in the map
func (m *OrderedMap[T, V]) Values() []V {
	return append([]V{}, m.values...)
}

// Keys returns all keys in the map
func (m *OrderedMap[T, V]) Keys() []T {
	keys := make([]T, len(m.keys))
	for k, v := range m.keys {
		keys[v] = k
	}
	return keys
}

// Len returns a number of the map
func (m *OrderedMap[T, V]) Len() int {
	return len(m.keys)
}
