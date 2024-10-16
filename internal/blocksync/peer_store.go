package blocksync

import (
	"math"
	"sync"
	"time"

	"golang.org/x/exp/constraints"

	"github.com/dashpay/tenderdash/internal/libs/flowrate"
	"github.com/dashpay/tenderdash/libs/store"
	"github.com/dashpay/tenderdash/types"
)

type (
	// InMemPeerStore in-memory peer store
	InMemPeerStore struct {
		mtx       sync.RWMutex
		store     store.Store[types.NodeID, PeerData]
		maxHeight int64
	}
	// PeerData uses to keep peer related data like base height and the current height etc
	PeerData struct {
		numPending  int32
		height      int64
		base        int64
		peerID      types.NodeID
		recvMonitor *flowrate.Monitor
		startAt     time.Time
	}
)

const (
	flowRateInitialValue = float64(minRecvRate) * math.E
)

// NewInMemPeerStore creates a new in-memory peer store
func NewInMemPeerStore(peers ...PeerData) *InMemPeerStore {
	mem := &InMemPeerStore{
		store: store.NewInMemStore[types.NodeID, PeerData](),
	}
	for _, peer := range peers {
		mem.Put(peer.peerID, peer)
	}
	return mem
}

// Get returns peer's data and true if the peer is found otherwise empty structure and false
func (p *InMemPeerStore) Get(peerID types.NodeID) (PeerData, bool) {
	return p.store.Get(peerID)
}

// GetAndDelete combines Get operation and Delete in one call
func (p *InMemPeerStore) GetAndDelete(peerID types.NodeID) (PeerData, bool) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	peer, found := p.store.GetAndDelete(peerID)
	if found && peer.height == p.maxHeight {
		p.updateMaxHeight()
	}
	return peer, found
}

// Put adds the peer data to the store if the peer does not exist, otherwise update the current value
func (p *InMemPeerStore) Put(peerID types.NodeID, newPeer PeerData) {
	p.store.Put(peerID, newPeer)
	p.mtx.Lock()
	defer p.mtx.Unlock()
	p.maxHeight = max(p.maxHeight, newPeer.height)
}

// Delete deletes the peer data from the store
func (p *InMemPeerStore) Delete(peerID types.NodeID) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	peer, found := p.store.GetAndDelete(peerID)
	if !found {
		return
	}
	if peer.height == p.maxHeight {
		p.updateMaxHeight()
	}
}

// MaxHeight looks at all the peers in the store to get the maximum peer height.
func (p *InMemPeerStore) MaxHeight() int64 {
	p.mtx.RLock()
	defer p.mtx.RUnlock()
	return p.maxHeight
}

// Update applies update functions to the peer if it exists
func (p *InMemPeerStore) Update(peerID types.NodeID, updates ...store.UpdateFunc[types.NodeID, PeerData]) {
	p.store.Update(peerID, updates...)
	peer, found := p.store.Get(peerID)
	if !found {
		return
	}
	p.mtx.Lock()
	defer p.mtx.Unlock()
	p.maxHeight = max(p.maxHeight, peer.height)
}

// Query finds and returns the copy of peers by specification conditions
func (p *InMemPeerStore) Query(spec store.QueryFunc[types.NodeID, PeerData], limit int) []PeerData {
	return p.store.Query(spec, limit)
}

// FindPeer finds a peer for the request
// criteria by which the peer is looked up:
// 1. the number of pending requests must be less allowed (maxPendingRequestsPerPeer)
// 2. the height must be between two values base and height
// otherwise return the empty peer data and false
func (p *InMemPeerStore) FindPeer(height int64) (PeerData, bool) {
	spec := store.AndX(
		peerNumPendingCond(maxPendingRequestsPerPeer, "<"),
		heightBetweenPeerHeightRange(height),
		ignoreTimedOutPeers(minRecvRate),
	)
	peers := p.Query(spec, 1)
	if len(peers) == 0 {
		return PeerData{}, false
	}
	return peers[0], true
}

// FindTimedoutPeers finds and returns the timed out peers
func (p *InMemPeerStore) FindTimedoutPeers() []PeerData {
	return p.Query(store.AndX(
		peerNumPendingCond(0, ">"),
		transferRateNotZeroAndLessMinRate(minRecvRate),
	), 0)
}

// All returns all stored peers in the store
func (p *InMemPeerStore) All() []PeerData {
	return p.store.All()
}

// Len returns the count of all stored peers
func (p *InMemPeerStore) Len() int {
	return p.store.Len()
}

// IsZero returns true if the store doesn't have a peer yet otherwise false
func (p *InMemPeerStore) IsZero() bool {
	return p.store.IsZero()
}

func (p *InMemPeerStore) updateMaxHeight() {
	p.maxHeight = 0
	for _, peer := range p.store.All() {
		p.maxHeight = max(p.maxHeight, peer.height)
	}
}

// TODO with fixed worker pool size this condition is not needed anymore
func peerNumPendingCond(val int32, op string) store.QueryFunc[types.NodeID, PeerData] {
	return func(_ types.NodeID, peer PeerData) bool {
		switch op {
		case "<":
			return peer.numPending < val
		case ">":
			return peer.numPending > val
		}
		panic("unsupported operation")
	}
}

func heightBetweenPeerHeightRange(height int64) store.QueryFunc[types.NodeID, PeerData] {
	return func(_ types.NodeID, peer PeerData) bool {
		return height >= peer.base && height <= peer.height
	}
}

func transferRateNotZeroAndLessMinRate(minRate int64) store.QueryFunc[types.NodeID, PeerData] {
	return func(_ types.NodeID, peer PeerData) bool {
		curRate := peer.recvMonitor.CurrentTransferRate()
		return curRate != 0 && curRate < minRate
	}
}

func ignoreTimedOutPeers(minRate int64) store.QueryFunc[types.NodeID, PeerData] {
	return func(_ types.NodeID, peer PeerData) bool {
		curRate := peer.recvMonitor.CurrentTransferRate()
		if curRate == 0 {
			return true
		}
		return curRate >= minRate
	}
}

func newPeerData(peerID types.NodeID, base, height int64) PeerData {
	startAt := time.Now()
	return PeerData{
		peerID:      peerID,
		base:        base,
		height:      height,
		recvMonitor: newPeerMonitor(startAt),
		startAt:     startAt,
	}
}

func newPeerMonitor(at time.Time) *flowrate.Monitor {
	m := flowrate.New(at, time.Second, time.Second*40)
	m.SetREMA(flowRateInitialValue)
	return m
}

// AddNumPending adds a value to the numPending field
func AddNumPending(val int32) store.UpdateFunc[types.NodeID, PeerData] {
	return func(_ types.NodeID, peer *PeerData) {
		peer.numPending += val
	}
}

// UpdateMonitor adds a block size value to the peer monitor if numPending is greater than zero
func UpdateMonitor(recvSize int) store.UpdateFunc[types.NodeID, PeerData] {
	return func(_ types.NodeID, peer *PeerData) {
		if peer.numPending > 0 {
			peer.recvMonitor.Update(recvSize)
		}
	}
}

// ResetMonitor replaces a peer monitor on a new one if numPending is zero
func ResetMonitor() store.UpdateFunc[types.NodeID, PeerData] {
	return func(_ types.NodeID, peer *PeerData) {
		if peer.numPending == 0 {
			peer.recvMonitor = newPeerMonitor(peer.startAt)
		}
	}
}

func max[T constraints.Ordered](a, b T) T {
	if a > b {
		return a
	}
	return b
}
