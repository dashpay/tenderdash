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
		numPending int32
		// numFailures counts block requests this peer failed in a row. It resets
		// on the first successful response.
		numFailures int32
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

// Upsert records the range of blocks a peer advertises, adding the peer if it is
// not known yet.
//
// The advertised base and height are the only things a peer tells us about itself;
// everything else held in PeerData - the count of requests we have issued and not
// yet accounted for, the run of consecutive failures, the receive-rate monitor and
// the time we first saw the peer - describes our own outstanding requests and the
// peer's service quality. Peers re-advertise their range every few seconds, far
// more often than a block request times out, so replacing those counters here
// would reset them faster than any threshold built on them can be reached and
// would leave requests in flight that nothing accounts for.
func (p *InMemPeerStore) Upsert(newPeer PeerData) {
	var (
		known      bool
		prevHeight int64
	)
	// Merged through the store's own update path, so it is atomic with respect to
	// the request accounting applied from the worker goroutines.
	p.store.Update(newPeer.peerID, func(_ types.NodeID, peer *PeerData) {
		known = true
		prevHeight = peer.height
		peer.base = newPeer.base
		peer.height = newPeer.height
	})
	if !known {
		p.Put(newPeer.peerID, newPeer)
		return
	}
	p.mtx.Lock()
	defer p.mtx.Unlock()
	if newPeer.height >= p.maxHeight {
		p.maxHeight = newPeer.height
		return
	}
	// The peer that held the highest advertised block just lowered its height -
	// its blocks were pruned, or it restarted from a snapshot. Keeping the old
	// value would leave the maximum pointing at a block no peer can serve, which
	// the synchronizer reads as "still behind" for as long as that peer stays
	// connected.
	if prevHeight == p.maxHeight {
		p.updateMaxHeight()
	}
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

// AddFailure records a block request that this peer failed to answer: the
// request stops being pending, and the peer's consecutive failure count grows.
//
// It reports true once the peer has failed maxFailures requests in a row and
// should be dropped. An unknown peer reports false, so the failures of a peer
// that was already removed do not report it again.
func (p *InMemPeerStore) AddFailure(peerID types.NodeID, maxFailures int32) bool {
	tooMany := false
	p.store.Update(peerID, func(_ types.NodeID, peer *PeerData) {
		peer.addPending(-1)
		peer.numFailures++
		tooMany = peer.numFailures >= maxFailures
	})
	return tooMany
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
	return func(peerID types.NodeID, peer PeerData) bool {
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
	return func(peerID types.NodeID, peer PeerData) bool {
		return height >= peer.base && height <= peer.height
	}
}

func transferRateNotZeroAndLessMinRate(minRate int64) store.QueryFunc[types.NodeID, PeerData] {
	return func(peerID types.NodeID, peer PeerData) bool {
		curRate := peer.recvMonitor.CurrentTransferRate()
		return curRate != 0 && curRate < minRate
	}
}

func ignoreTimedOutPeers(minRate int64) store.QueryFunc[types.NodeID, PeerData] {
	return func(peerID types.NodeID, peer PeerData) bool {
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
	return func(peerID types.NodeID, peer *PeerData) {
		peer.addPending(val)
	}
}

// addPending changes the count of block requests issued to this peer that have
// not been accounted for yet, and never lets it fall below zero.
//
// The count only grows when a request is issued and only shrinks when one is
// answered or fails, so a negative value is always a bug in the accounting. It is
// floored here rather than at the call sites because there are several of them and
// the consequences are silent: a negative count always satisfies the per-peer
// request limit, so the peer is handed unlimited concurrent requests, and it never
// satisfies the slow-peer check, so the peer can no longer be evicted for being
// too slow.
func (p *PeerData) addPending(delta int32) {
	p.numPending += delta
	if p.numPending < 0 {
		p.numPending = 0
	}
}

// ResetFailures clears the count of consecutive failed requests, so that the
// threshold only ever trips on an unbroken run of failures
func ResetFailures() store.UpdateFunc[types.NodeID, PeerData] {
	return func(_ types.NodeID, peer *PeerData) {
		peer.numFailures = 0
	}
}

// UpdateMonitor adds a block size value to the peer monitor if numPending is greater than zero
func UpdateMonitor(recvSize int) store.UpdateFunc[types.NodeID, PeerData] {
	return func(peerID types.NodeID, peer *PeerData) {
		if peer.numPending > 0 {
			peer.recvMonitor.Update(recvSize)
		}
	}
}

// ResetMonitor replaces a peer monitor on a new one if numPending is zero
func ResetMonitor() store.UpdateFunc[types.NodeID, PeerData] {
	return func(peerID types.NodeID, peer *PeerData) {
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
