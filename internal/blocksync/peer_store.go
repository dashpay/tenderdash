package blocksync

import (
	"github.com/jonboulle/clockwork"
	"golang.org/x/exp/constraints"

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
		clock clockwork.Clock
	}
	// PeerData uses to keep peer related data like base height and the current height etc
	PeerData struct {
		numPending int32
		service    *peerService
		// numFailures counts block requests this peer failed in a row. It resets
		// on the first successful response.
		numFailures int32
		height      int64
		base        int64
		// missingThrough records unavailable history independently of advertised status.
		missingThrough int64
		peerID         types.NodeID
	}
)

// NewInMemPeerStore creates a new in-memory peer store
func NewInMemPeerStore(peers ...PeerData) *InMemPeerStore {
	mem := &InMemPeerStore{
		store: store.NewInMemStore[types.NodeID, PeerData](),
		clock: clockwork.NewRealClock(),
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
	newPeer.service.setClock(p.clock)
	p.store.Put(peerID, newPeer)
}

// Upsert records the range of blocks a peer advertises, adding the peer if it is
// not known yet.
//
// The advertised base and height are the only things a peer tells us about itself;
// everything else held in PeerData - the count of requests we have issued and not
// yet accounted for, the run of consecutive failures and unavailable history -
// describes our own outstanding requests and the peer's service quality.
// Peers re-advertise their range every few seconds, far
// more often than a block request times out, so replacing those counters here
// would reset them faster than any threshold built on them can be reached and
// would leave requests in flight that nothing accounts for.
//
// Insert and merge are a single store operation. Peers are made known by the p2p
// consumer goroutine while the job producer is already issuing requests against
// them, so a lookup followed by a separate write would let a peer inserted in
// between be overwritten - losing precisely the state this exists to keep.
func (p *InMemPeerStore) Upsert(newPeer PeerData) {
	newPeer.service.setClock(p.clock)
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
// will never arrive. The scan is O(peers), the same order as FindPeer,
// which the producing loop already runs for every job.
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

// FindPeer prefers ordinary service except when a bounded recovery probe is due.
func (p *InMemPeerStore) FindPeer(height int64) (PeerData, bool) {
	peers := p.Query(store.AndX(peerNumPendingCond(maxPendingRequestsPerPeer, "<"),
		heightBetweenPeerHeightRange(height)), 0)
	var selected PeerData
	priority := 0
	for _, peer := range peers {
		if next := peer.service.priority(peer.numPending, false); next > priority {
			selected, priority = peer, next
		}
	}
	return selected, priority > 0
}

// reservePeer checks and reserves both pending capacity and the single probe
// under the store lock. Concurrent producers cannot reserve the same probe.
func (p *InMemPeerStore) reservePeer(height int64) (PeerData, bool) {
	peer, found := p.FindPeer(height)
	if !found {
		return PeerData{}, false
	}
	reserved := false
	p.store.Update(peer.peerID, func(_ types.NodeID, current *PeerData) {
		if current.service != peer.service || current.numPending >= maxPendingRequestsPerPeer ||
			!heightBetweenPeerHeightRange(height)(current.peerID, *current) ||
			current.service.priority(current.numPending, true) == 0 {
			return
		}
		current.addPending(1)
		peer = *current
		reserved = true
	})
	return peer, reserved
}

// HasPeerForHeight reports whether a peer advertises the height without having
// reported it missing. A peer at its pending-request limit is temporarily busy,
// so it must still count when deciding whether block sync can make progress.
func (p *InMemPeerStore) HasPeerForHeight(height int64) bool {
	return len(p.Query(heightBetweenPeerHeightRange(height), 1)) > 0
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

// RecordMissingBlock completes a request and excludes the peer's unavailable history.
func (p *InMemPeerStore) RecordMissingBlock(peerID types.NodeID, height int64) {
	p.store.Update(peerID, func(_ types.NodeID, peer *PeerData) {
		peer.addPending(-1)
		peer.missingThrough = max(peer.missingThrough, height)
	})
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
		return height >= peer.base && height <= peer.height && height > peer.missingThrough
	}
}

func newPeerData(peerID types.NodeID, base, height int64) PeerData {
	return PeerData{peerID: peerID, base: base, height: height, service: newPeerService(clockwork.NewRealClock())}
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
// request limit, so the peer can be handed too many concurrent requests.
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

func max[T constraints.Ordered](a, b T) T {
	if a > b {
		return a
	}
	return b
}
