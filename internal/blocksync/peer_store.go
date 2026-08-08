package blocksync

import (
	"math"
	"time"

	"golang.org/x/exp/constraints"

	"github.com/dashpay/tenderdash/internal/libs/flowrate"
	"github.com/dashpay/tenderdash/libs/store"
	"github.com/dashpay/tenderdash/types"
)

type (
	// InMemPeerStore in-memory peer store
	InMemPeerStore struct {
		// Held as the concrete store rather than the store.Store interface so that
		// Upsert can insert-or-merge in a single locked operation; composing that
		// out of the interface's Get, Put and Update reintroduces a window in which
		// a concurrent write is lost. Nothing here substitutes another store.
		store *store.InMemStore[types.NodeID, PeerData]
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
	return p.store.GetAndDelete(peerID)
}

// Put adds the peer data to the store if the peer does not exist, otherwise update the current value
func (p *InMemPeerStore) Put(peerID types.NodeID, newPeer PeerData) {
	p.store.Put(peerID, newPeer)
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
//
// Insert and merge are a single store operation. Peers are made known by the p2p
// consumer goroutine while the job producer is already issuing requests against
// them, so a lookup followed by a separate write would let a peer inserted in
// between be overwritten - losing precisely the state this exists to keep.
func (p *InMemPeerStore) Upsert(newPeer PeerData) {
	p.store.Upsert(newPeer.peerID, newPeer, func(_ types.NodeID, peer *PeerData) {
		peer.base = newPeer.base
		peer.height = newPeer.height
	})
}

// Delete deletes the peer data from the store
func (p *InMemPeerStore) Delete(peerID types.NodeID) {
	p.store.Delete(peerID)
}

// MaxHeight looks at all the peers in the store to get the maximum peer height.
//
// It is derived on read rather than cached. A cached maximum has to be maintained
// by every path that adds, updates or removes a peer, each holding its own lock,
// so two of them interleaving can publish a height that belonged to a peer already
// gone or already lowered. This value decides whether the node considers itself
// caught up and whether a stalled sync gives up, so a height no peer can serve does
// not merely misreport - it keeps the node in block sync waiting for a block that
// will never arrive. The scan is O(peers), the same order as FindPeer and
// FindTimedoutPeers, which the producing loop already runs for every job.
func (p *InMemPeerStore) MaxHeight() int64 {
	var maxHeight int64
	// All takes a snapshot under the store's lock, so the result is the maximum of
	// a state the store actually held, not a mix of several.
	for _, peer := range p.store.All() {
		maxHeight = max(maxHeight, peer.height)
	}
	return maxHeight
}

// Update applies update functions to the peer if it exists
func (p *InMemPeerStore) Update(peerID types.NodeID, updates ...store.UpdateFunc[types.NodeID, PeerData]) {
	p.store.Update(peerID, updates...)
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
