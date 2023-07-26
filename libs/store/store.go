//go:generate ../../scripts/mockery_generate.sh Store

package store

import (
	sync "github.com/sasha-s/go-deadlock"
	"github.com/tendermint/tendermint/libs/ds"
)

type (
	Store[K comparable, V any] interface {
		Get(key K) (V, bool)
		GetAndDelete(key K) (V, bool)
		Put(key K, data V)
		Delete(key K)
		Update(key K, updates ...UpdateFunc[K, V])
		Query(spec QueryFunc[K, V], limit int) []V
		All() []V
		Len() int
		IsZero() bool
	}
	// QueryFunc is a function type for a specification function
	QueryFunc[K comparable, V any] func(key K, data V) bool
	// UpdateFunc is a function type for an item update functions
	UpdateFunc[K comparable, V any] func(key K, item *V)
	// InMemStore in-memory peer store
	InMemStore[K comparable, T any] struct {
		mtx   sync.RWMutex
		items *ds.OrderedMap[K, T]
	}
)

// NewInMemStore creates a new in-memory peer store
func NewInMemStore[K comparable, V any]() *InMemStore[K, V] {
	mem := &InMemStore[K, V]{
		items: ds.NewOrderedMap[K, V](),
	}
	return mem
}

// Get returns peer's data and true if the peer is found otherwise empty structure and false
func (p *InMemStore[K, T]) Get(key K) (T, bool) {
	p.mtx.RLock()
	defer p.mtx.RUnlock()
	return p.items.Get(key)
}

// GetAndDelete combines Get operation and Delete in one call
func (p *InMemStore[K, T]) GetAndDelete(key K) (T, bool) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	val, found := p.items.Get(key)
	if found {
		p.items.Delete(key)
		return val, true
	}
	var zero T
	return zero, found
}

// Put adds the peer data to the store if the peer does not exist, otherwise update the current value
func (p *InMemStore[K, T]) Put(key K, data T) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	p.items.Put(key, data)
}

// Delete removes the peer data from the store
func (p *InMemStore[K, T]) Delete(key K) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	p.items.Delete(key)
}

// Update applies update functions to the peer if it exists
func (p *InMemStore[K, T]) Update(key K, updates ...UpdateFunc[K, T]) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	val, found := p.items.Get(key)
	if !found {
		return
	}
	for _, update := range updates {
		update(key, &val)
	}
	p.items.Put(key, val)
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
	return p.items.Values()
}

// Len returns the count of all stored values
func (p *InMemStore[K, T]) Len() int {
	p.mtx.RLock()
	defer p.mtx.RUnlock()
	return p.items.Len()
}

// IsZero returns true if the store doesn't have a peer yet otherwise false
func (p *InMemStore[K, T]) IsZero() bool {
	return p.Len() == 0
}

func (p *InMemStore[K, T]) query(spec QueryFunc[K, T], limit int) []T {
	var res []T
	keys := p.items.Keys()
	vals := p.items.Values()
	for i := 0; i < len(keys); i++ {
		if spec(keys[i], vals[i]) {
			res = append(res, vals[i])
			if limit > 0 && limit == len(res) {
				return res
			}
		}
	}
	return res
}
