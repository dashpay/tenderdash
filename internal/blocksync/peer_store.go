package blocksync

import (
	"math"
	"sync"
	"time"

	"golang.org/x/exp/constraints"

	"github.com/tendermint/tendermint/internal/libs/flowrate"
	"github.com/tendermint/tendermint/types"
)

type (
	// PeerQueryFunc is a function type for peer specification function
	PeerQueryFunc func(peer PeerData) bool
	// PeerUpdateFunc is a function type for peer update functions
	PeerUpdateFunc func(peer *PeerData)
	// InMemPeerStore in-memory peer store
	InMemPeerStore struct {
		mtx       sync.RWMutex
		peerIDx   map[types.NodeID]int
		peers     []*PeerData
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
		peerIDx: make(map[types.NodeID]int),
	}
	for _, peer := range peers {
		mem.Put(peer)
	}
	return mem
}

// Get returns peer's data and true if the peer is found otherwise empty structure and false
func (p *InMemPeerStore) Get(peerID types.NodeID) (PeerData, bool) {
	p.mtx.RLock()
	defer p.mtx.RUnlock()
	peer, found := p.get(peerID)
	if found {
		return *peer, true
	}
	return PeerData{}, false
}

// GetAndRemove combines Get operation and Remove in one call
func (p *InMemPeerStore) GetAndRemove(peerID types.NodeID) (PeerData, bool) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	peer, found := p.get(peerID)
	if found {
		p.remove(peerID)
		return *peer, true
	}
	return PeerData{}, found
}

// Put adds the peer data to the store if the peer does not exist, otherwise update the current value
func (p *InMemPeerStore) Put(newPeer PeerData) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	_, ok := p.get(newPeer.peerID)
	if !ok {
		p.peers = append(p.peers, &newPeer)
		p.peerIDx[newPeer.peerID] = len(p.peers) - 1
		p.maxHeight = max(p.maxHeight, newPeer.height)
		return
	}
	p.update(newPeer)
}

// Remove removes the peer data from the store
func (p *InMemPeerStore) Remove(peerID types.NodeID) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	p.remove(peerID)
}

// MaxHeight looks at all the peers in the store to get the maximum peer height.
func (p *InMemPeerStore) MaxHeight() int64 {
	p.mtx.RLock()
	defer p.mtx.RUnlock()
	return p.maxHeight
}

// PeerUpdate applies update functions to the peer if it exists
func (p *InMemPeerStore) PeerUpdate(peerID types.NodeID, updates ...PeerUpdateFunc) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	peer, found := p.get(peerID)
	if !found {
		return
	}
	for _, update := range updates {
		update(peer)
	}
}

// Query finds and returns the copy of peers by specification conditions
func (p *InMemPeerStore) Query(spec PeerQueryFunc, limit int) []*PeerData {
	p.mtx.RLock()
	defer p.mtx.RUnlock()
	return p.query(spec, limit)
}

// FindPeer finds a peer for the request
// criteria by which the peer is looked up:
// 1. the number of pending requests must be less allowed (maxPendingRequestsPerPeer)
// 2. the height must be between two values base and height
// otherwise return the empty peer data and false
func (p *InMemPeerStore) FindPeer(height int64) (PeerData, bool) {
	spec := andX(
		peerNumPendingCond(maxPendingRequestsPerPeer, "<"),
		heightBetweenPeerHeightRange(height),
		ignoreTimedOutPeers(minRecvRate),
	)
	peers := p.Query(spec, 1)
	if len(peers) == 0 {
		return PeerData{}, false
	}
	return *peers[0], true
}

// FindTimedoutPeers finds and returns the timed out peers
func (p *InMemPeerStore) FindTimedoutPeers() []*PeerData {
	return p.Query(andX(
		peerNumPendingCond(0, ">"),
		transferRateNotZeroAndLessMinRate(minRecvRate),
	), 0)
}

// All returns all stored peers in the store
func (p *InMemPeerStore) All() []PeerData {
	p.mtx.RLock()
	defer p.mtx.RUnlock()
	ret := make([]PeerData, len(p.peers))
	for i, peer := range p.peers {
		ret[i] = *peer
	}
	return ret
}

// Len returns the count of all stored peers
func (p *InMemPeerStore) Len() int {
	p.mtx.RLock()
	defer p.mtx.RUnlock()
	return len(p.peers)
}

// IsZero returns true if the store doesn't have a peer yet otherwise false
func (p *InMemPeerStore) IsZero() bool {
	return p.Len() == 0
}

func (p *InMemPeerStore) get(peerID types.NodeID) (*PeerData, bool) {
	i, ok := p.peerIDx[peerID]
	if !ok {
		return nil, false
	}
	return p.peers[i], true
}

func (p *InMemPeerStore) update(peer PeerData) {
	i, ok := p.peerIDx[peer.peerID]
	if !ok {
		return
	}
	p.peers[i].height = peer.height
	p.peers[i].base = peer.base
	p.maxHeight = max(p.maxHeight, peer.height)
}

func (p *InMemPeerStore) remove(peerID types.NodeID) {
	i, ok := p.peerIDx[peerID]
	if !ok {
		return
	}
	peer := p.peers[i]
	right := p.peers[i+1:]
	for j, peer := range right {
		p.peerIDx[peer.peerID] = i + j
	}
	left := p.peers[0:i]
	p.peers = append(left, right...)
	delete(p.peerIDx, peerID)
	if peer.height == p.maxHeight {
		p.updateMaxHeight()
	}
}

func (p *InMemPeerStore) query(spec PeerQueryFunc, limit int) []*PeerData {
	var res []*PeerData
	for _, i := range p.peerIDx {
		peer := p.peers[i]
		if spec(*peer) {
			c := *peer
			res = append(res, &c)
			if limit > 0 && limit == len(res) {
				return res
			}
		}
	}
	return res
}

func (p *InMemPeerStore) updateMaxHeight() {
	p.maxHeight = 0
	for _, peer := range p.peers {
		p.maxHeight = max(p.maxHeight, peer.height)
	}
}

// TODO with fixed worker pool size this condition is not needed anymore
func peerNumPendingCond(val int32, op string) PeerQueryFunc {
	return func(peer PeerData) bool {
		switch op {
		case "<":
			return peer.numPending < val
		case ">":
			return peer.numPending > val
		}
		panic("unsupported operation")
	}
}

func heightBetweenPeerHeightRange(height int64) PeerQueryFunc {
	return func(peer PeerData) bool {
		return height >= peer.base && height <= peer.height
	}
}

func transferRateNotZeroAndLessMinRate(minRate int64) PeerQueryFunc {
	return func(peer PeerData) bool {
		curRate := peer.recvMonitor.CurrentTransferRate()
		return curRate != 0 && curRate < minRate
	}
}

func ignoreTimedOutPeers(minRate int64) PeerQueryFunc {
	return func(peer PeerData) bool {
		curRate := peer.recvMonitor.CurrentTransferRate()
		if curRate == 0 {
			return true
		}
		return curRate >= minRate
	}
}

func andX(specs ...PeerQueryFunc) PeerQueryFunc {
	return func(peer PeerData) bool {
		for _, spec := range specs {
			if !spec(peer) {
				return false
			}
		}
		return true
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
func AddNumPending(val int32) PeerUpdateFunc {
	return func(peer *PeerData) {
		peer.numPending += val
	}
}

// UpdateMonitor adds a block size value to the peer monitor if numPending is greater than zero
func UpdateMonitor(recvSize int) PeerUpdateFunc {
	return func(peer *PeerData) {
		if peer.numPending > 0 {
			peer.recvMonitor.Update(recvSize)
		}
	}
}

// ResetMonitor replaces a peer monitor on a new one if numPending is zero
func ResetMonitor() PeerUpdateFunc {
	return func(peer *PeerData) {
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
