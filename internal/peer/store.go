//go:generate ../../scripts/mockery_generate.sh Store

package peer

import (
	sync "github.com/sasha-s/go-deadlock"
)

type (
	Store[K comparable, T any] interface {
		Get(key K) (T, bool)
		GetAndRemove(key K) (T, bool)
		Put(key K, data T)
		Remove(key K)
		Update(key K, updates ...UpdateFunc[K, T])
		Query(spec QueryFunc[K, T], limit int) []*T
		All() []T
	}
	// QueryFunc is a function type for a specification function
	QueryFunc[K comparable, T any] func(key K, data T) bool
	// UpdateFunc is a function type for an item update functions
	UpdateFunc[K comparable, T any] func(key K, item *T)
	// InMemStore in-memory peer store
	InMemStore[K comparable, T any] struct {
		mtx    sync.RWMutex
		keyIDx map[K]int
		values []*item[K, T]
	}
	item[K comparable, T any] struct {
		key  K
		data T
	}
)

// NewInMemStore creates a new in-memory peer store
func NewInMemStore[K comparable, T any]() *InMemStore[K, T] {
	mem := &InMemStore[K, T]{
		keyIDx: make(map[K]int),
	}
	return mem
}

// Get returns peer's data and true if the peer is found otherwise empty structure and false
func (p *InMemStore[K, T]) Get(key K) (T, bool) {
	p.mtx.RLock()
	defer p.mtx.RUnlock()
	peer, found := p.get(key)
	if found {
		return peer.data, true
	}
	var zero T
	return zero, false
}

// GetAndRemove combines Get operation and Remove in one call
func (p *InMemStore[K, T]) GetAndRemove(key K) (T, bool) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	peer, found := p.get(key)
	if found {
		p.remove(key)
		return peer.data, true
	}
	var zero T
	return zero, found
}

// Put adds the peer data to the store if the peer does not exist, otherwise update the current value
func (p *InMemStore[K, T]) Put(key K, data T) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	_, ok := p.get(key)
	it := item[K, T]{key: key, data: data}
	if !ok {
		p.values = append(p.values, &it)
		p.keyIDx[key] = len(p.values) - 1
		return
	}
	p.update(it)
}

// Remove removes the peer data from the store
func (p *InMemStore[K, T]) Remove(key K) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	p.remove(key)
}

// Update applies update functions to the peer if it exists
func (p *InMemStore[K, T]) Update(key K, updates ...UpdateFunc[K, T]) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	peer, found := p.get(key)
	if !found {
		return
	}
	for _, update := range updates {
		update(peer.key, &peer.data)
	}
}

// Query finds and returns the copy of values by specification conditions
func (p *InMemStore[K, T]) Query(spec QueryFunc[K, T], limit int) []T {
	p.mtx.RLock()
	defer p.mtx.RUnlock()
	return p.query(spec, limit)
}

// All returns all stored values in the store
func (p *InMemStore[K, T]) All() []T {
	p.mtx.RLock()
	defer p.mtx.RUnlock()
	ret := make([]T, len(p.values))
	for i, peer := range p.values {
		ret[i] = peer.data
	}
	return ret
}

// Len returns the count of all stored values
func (p *InMemStore[K, T]) Len() int {
	p.mtx.RLock()
	defer p.mtx.RUnlock()
	return len(p.values)
}

// IsZero returns true if the store doesn't have a peer yet otherwise false
func (p *InMemStore[K, T]) IsZero() bool {
	return p.Len() == 0
}

func (p *InMemStore[K, T]) get(key K) (*item[K, T], bool) {
	i, ok := p.keyIDx[key]
	if !ok {
		return nil, false
	}
	return p.values[i], true
}

func (p *InMemStore[K, T]) update(item item[K, T]) {
	i, ok := p.keyIDx[item.key]
	if !ok {
		return
	}
	p.values[i] = &item
}

func (p *InMemStore[K, T]) remove(key K) {
	i, ok := p.keyIDx[key]
	if !ok {
		return
	}
	right := p.values[i+1:]
	for j, peer := range right {
		p.keyIDx[peer.key] = i + j
	}
	left := p.values[0:i]
	p.values = append(left, right...)
	delete(p.keyIDx, key)
}

func (p *InMemStore[K, T]) query(spec QueryFunc[K, T], limit int) []T {
	var res []T
	for _, it := range p.values {
		if spec(it.key, it.data) {
			c := it.data
			res = append(res, c)
			if limit > 0 && limit == len(res) {
				return res
			}
		}
	}
	return res
}
