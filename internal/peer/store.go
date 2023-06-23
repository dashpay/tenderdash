//go:generate ../../scripts/mockery_generate.sh Store

package peer

import (
	sync "github.com/sasha-s/go-deadlock"

	"github.com/tendermint/tendermint/types"
)

type (
	Store[T any] interface {
		Get(peerID types.NodeID) (T, bool)
		GetAndRemove(peerID types.NodeID) (T, bool)
		Put(peerID types.NodeID, data T)
		Remove(peerID types.NodeID)
		Update(peerID types.NodeID, updates ...UpdateFunc[T])
		Query(spec QueryFunc[T], limit int) []*T
		All() []T
	}
	// QueryFunc is a function type for peer specification function
	QueryFunc[T any] func(peerID types.NodeID, data T) bool
	// UpdateFunc is a function type for peer update functions
	UpdateFunc[T any] func(peerID types.NodeID, item *T)
	// InMemStore in-memory peer store
	InMemStore[T any] struct {
		mtx     sync.RWMutex
		peerIDx map[types.NodeID]int
		peers   []*item[T]
	}
	item[T any] struct {
		peerID types.NodeID
		data   T
	}
)

// NewInMemStore creates a new in-memory peer store
func NewInMemStore[T any]() *InMemStore[T] {
	mem := &InMemStore[T]{
		peerIDx: make(map[types.NodeID]int),
	}
	return mem
}

// Get returns peer's data and true if the peer is found otherwise empty structure and false
func (p *InMemStore[T]) Get(peerID types.NodeID) (T, bool) {
	p.mtx.RLock()
	defer p.mtx.RUnlock()
	peer, found := p.get(peerID)
	if found {
		return peer.data, true
	}
	var zero T
	return zero, false
}

// GetAndRemove combines Get operation and Remove in one call
func (p *InMemStore[T]) GetAndRemove(peerID types.NodeID) (T, bool) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	peer, found := p.get(peerID)
	if found {
		p.remove(peerID)
		return peer.data, true
	}
	var zero T
	return zero, found
}

// Put adds the peer data to the store if the peer does not exist, otherwise update the current value
func (p *InMemStore[T]) Put(peerID types.NodeID, data T) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	_, ok := p.get(peerID)
	peerItem := item[T]{peerID: peerID, data: data}
	if !ok {
		p.peers = append(p.peers, &peerItem)
		p.peerIDx[peerID] = len(p.peers) - 1
		return
	}
	p.update(peerItem)
}

// Remove removes the peer data from the store
func (p *InMemStore[T]) Remove(peerID types.NodeID) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	p.remove(peerID)
}

// Update applies update functions to the peer if it exists
func (p *InMemStore[T]) Update(peerID types.NodeID, updates ...UpdateFunc[T]) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	peer, found := p.get(peerID)
	if !found {
		return
	}
	for _, update := range updates {
		update(peer.peerID, &peer.data)
	}
}

// Query finds and returns the copy of peers by specification conditions
func (p *InMemStore[T]) Query(spec QueryFunc[T], limit int) []*T {
	p.mtx.RLock()
	defer p.mtx.RUnlock()
	return p.query(spec, limit)
}

// All returns all stored peers in the store
func (p *InMemStore[T]) All() []T {
	p.mtx.RLock()
	defer p.mtx.RUnlock()
	ret := make([]T, len(p.peers))
	for i, peer := range p.peers {
		ret[i] = peer.data
	}
	return ret
}

// Len returns the count of all stored peers
func (p *InMemStore[T]) Len() int {
	p.mtx.RLock()
	defer p.mtx.RUnlock()
	return len(p.peers)
}

// IsZero returns true if the store doesn't have a peer yet otherwise false
func (p *InMemStore[T]) IsZero() bool {
	return p.Len() == 0
}

func (p *InMemStore[T]) get(peerID types.NodeID) (*item[T], bool) {
	i, ok := p.peerIDx[peerID]
	if !ok {
		return nil, false
	}
	return p.peers[i], true
}

func (p *InMemStore[T]) update(item item[T]) {
	i, ok := p.peerIDx[item.peerID]
	if !ok {
		return
	}
	p.peers[i] = &item
}

func (p *InMemStore[T]) remove(peerID types.NodeID) {
	i, ok := p.peerIDx[peerID]
	if !ok {
		return
	}
	right := p.peers[i+1:]
	for j, peer := range right {
		p.peerIDx[peer.peerID] = i + j
	}
	left := p.peers[0:i]
	p.peers = append(left, right...)
	delete(p.peerIDx, peerID)
}

func (p *InMemStore[T]) query(spec QueryFunc[T], limit int) []*T {
	var res []*T
	for _, i := range p.peerIDx {
		peer := p.peers[i]
		if spec(peer.peerID, peer.data) {
			c := peer.data
			res = append(res, &c)
			if limit > 0 && limit == len(res) {
				return res
			}
		}
	}
	return res
}
