package blocksync

import (
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tendermint/tendermint/internal/libs/flowrate"
	"github.com/tendermint/tendermint/types"
)

type (
	PeerQueryFunc  func(peer *PeerData) bool
	InMemPeerStore struct {
		mtx     sync.RWMutex
		peerIDx map[types.NodeID]int
		peers   []*PeerData
	}
)

// NewInMemPeerStore ...
func NewInMemPeerStore(peers ...*PeerData) *InMemPeerStore {
	mem := &InMemPeerStore{
		peerIDx: make(map[types.NodeID]int),
	}
	for _, peer := range peers {
		mem.Put(peer)
	}
	return mem
}

// Get ...
func (p *InMemPeerStore) Get(peerID types.NodeID) (*PeerData, bool) {
	p.mtx.RLock()
	defer p.mtx.RUnlock()
	return p.get(peerID)
}

// GetAndRemove ...
func (p *InMemPeerStore) GetAndRemove(peerID types.NodeID) (*PeerData, bool) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	peer, ok := p.get(peerID)
	if ok {
		p.remove(peerID)
	}
	return peer, ok
}

// Put ...
func (p *InMemPeerStore) Put(newPeer *PeerData) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	_, ok := p.get(newPeer.peerID)
	if !ok {
		p.peers = append(p.peers, newPeer)
		p.peerIDx[newPeer.peerID] = len(p.peers) - 1
		return
	}
	p.update(newPeer)
}

// Remove ...
func (p *InMemPeerStore) Remove(peerID types.NodeID) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	p.remove(peerID)
}

// MaxPeerHeight if no peers are left, maxPeerHeight is set to 0.
func (p *InMemPeerStore) MaxPeerHeight() int64 {
	var max int64
	for _, peer := range p.peers {
		if peer.height > max {
			max = peer.height
		}
	}
	return max
}

// Update ...
func (p *InMemPeerStore) Update(peer *PeerData) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	p.update(peer)
}

// Query ...
func (p *InMemPeerStore) Query(spec PeerQueryFunc) []*PeerData {
	p.mtx.RLock()
	defer p.mtx.RUnlock()
	return p.query(spec)
}

// FindPeer finds a peer for the request
// criteria by which the peer is looked up:
// 1. the peer is not timed out,
// 2. the number of pending requests must be less allowed (maxPendingRequestsPerPeer)
// 3. the height must be between two values base and height
// otherwise return nil
func (p *InMemPeerStore) FindPeer(height int64) *PeerData {
	peers := p.Query(andX(
		peerIsTimedout(false),
		peerNumPendingCond(maxPendingRequestsPerPeer, "<"),
		heightBetweenPeerHeightRange(height),
	))
	if len(peers) == 0 {
		return nil
	}
	return peers[0]
}

// FindTimedoutPeers ...
func (p *InMemPeerStore) FindTimedoutPeers() []*PeerData {
	return p.Query(orX(
		peerIsTimedout(true),
		andX(
			peerIsTimedout(false),
			peerNumPendingCond(0, ">"),
			transferRateNotZeroAndLessMinRate(),
		),
	))
}

// All ...
func (p *InMemPeerStore) All() []*PeerData {
	p.mtx.RLock()
	defer p.mtx.RUnlock()
	return append([]*PeerData(nil), p.peers...)
}

func (p *InMemPeerStore) Len() int {
	p.mtx.RLock()
	defer p.mtx.RUnlock()
	return len(p.peers)
}

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

func (p *InMemPeerStore) update(peer *PeerData) {
	i, ok := p.peerIDx[peer.peerID]
	if !ok {
		return
	}
	p.peers[i].height = peer.height
	p.peers[i].base = peer.base
}

func (p *InMemPeerStore) remove(peerID types.NodeID) {
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

func (p *InMemPeerStore) query(spec PeerQueryFunc) []*PeerData {
	var res []*PeerData
	for _, i := range p.peerIDx {
		peer := p.peers[i]
		if spec(peer) {
			res = append(res, peer)
		}
	}
	return res
}

func peerIsTimedout(timedout bool) PeerQueryFunc {
	return func(peer *PeerData) bool {
		return peer.didTimeout.Load() == timedout
	}
}

// TODO with fixed worker pool size this condition is not needed anymore
func peerNumPendingCond(val int32, op string) PeerQueryFunc {
	return func(peer *PeerData) bool {
		numPending := peer.numPending.Load()
		switch op {
		case "<":
			return numPending < val
		case ">":
			return numPending > val
		}
		panic("unsupported operation")
	}
}

func heightBetweenPeerHeightRange(height int64) PeerQueryFunc {
	return func(peer *PeerData) bool {
		return height >= peer.base && height <= peer.height
	}
}

func transferRateNotZeroAndLessMinRate() PeerQueryFunc {
	return func(peer *PeerData) bool {
		curRate := peer.recvMonitor.CurrentTransferRate()
		return curRate != 0 && curRate < minRecvRate
	}
}

func andX(specs ...PeerQueryFunc) PeerQueryFunc {
	return func(peer *PeerData) bool {
		for _, spec := range specs {
			if !spec(peer) {
				return false
			}
		}
		return true
	}
}

func orX(specs ...PeerQueryFunc) PeerQueryFunc {
	return func(peer *PeerData) bool {
		for _, spec := range specs {
			if spec(peer) {
				return true
			}
		}
		return false
	}
}

// PeerData ...
type PeerData struct {
	didTimeout  *atomic.Bool
	numPending  *atomic.Int32
	height      int64
	base        int64
	peerID      types.NodeID
	recvMonitor *flowrate.Monitor
	startAt     time.Time
}

func newPeerData(peerID types.NodeID, base, height int64) *PeerData {
	startAt := time.Now()
	initialValue := float64(minRecvRate) * math.E
	recvMonitor := flowrate.New(startAt, time.Second, time.Second*40)
	recvMonitor.SetREMA(initialValue)
	return &PeerData{
		peerID:      peerID,
		base:        base,
		height:      height,
		didTimeout:  &atomic.Bool{},
		numPending:  &atomic.Int32{},
		recvMonitor: recvMonitor,
		startAt:     startAt,
	}
}

func (p *PeerData) resetMonitor() {
	p.recvMonitor = flowrate.New(p.startAt, time.Second, time.Second*40)
	initialValue := float64(minRecvRate) * math.E
	p.recvMonitor.SetREMA(initialValue)
}

func (p *PeerData) IncrPending() {
	if p.numPending.Load() == 0 {
		p.resetMonitor()
	}
	p.numPending.Add(1)
}

func (p *PeerData) DecrPending(recvSize int) {
	p.numPending.Add(-1)
	if p.numPending.Load() > 0 {
		p.recvMonitor.Update(recvSize)
	}
}

func (p *PeerData) NumPending() int32 {
	return p.numPending.Load()
}
