package statesync

import (
	"crypto/sha256"
	"encoding/binary"
	"math/rand"
	"sort"
	"strings"

	sync "github.com/sasha-s/go-deadlock"

	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/types"
)

const (
	maxSnapshotBytes        = 64 * 1024 * 1024
	maxPeerSnapshotBytes    = 40_000_000
	maxSnapshots            = 1024
	maxSnapshotAssociations = maxSnapshots * recentSnapshots
	maxDiscoveryPeers       = 16
)

// snapshotKey is a snapshot key used for lookups.
type snapshotKey [sha256.Size]byte

// snapshot contains data about a snapshot.
type snapshot struct {
	Height   uint64
	Version  uint32
	Hash     tmbytes.HexBytes
	Metadata []byte

	trustedAppHash tmbytes.HexBytes // populated by light client
}

// Key generates a snapshot key, used for lookups. It takes into account not only the height and
// format, but also the chunks, hash, and metadata in case peers have generated snapshots in a
// non-deterministic manner. All fields must be equal for the snapshot to be considered the same.
func (s *snapshot) Key() snapshotKey {
	// Hash.Write() never returns an error.
	hasher := sha256.New()

	bz := make([]byte, 0, (64+32+32)/8)
	bz = binary.LittleEndian.AppendUint64(bz, s.Height)
	bz = binary.LittleEndian.AppendUint32(bz, s.Version)
	hasher.Write(bz)
	hasher.Write(s.Hash)
	hasher.Write(s.Metadata)
	var key snapshotKey
	copy(key[:], hasher.Sum(nil))
	return key
}

// snapshotPool discovers and aggregates snapshots across peers.
type snapshotPool struct {
	sync.Mutex
	snapshots     map[snapshotKey]*snapshot
	snapshotPeers map[snapshotKey]map[types.NodeID]types.NodeID

	// indexes for fast searches
	versionIndex map[uint32]map[snapshotKey]bool
	heightIndex  map[uint64]map[snapshotKey]bool
	peerIndex    map[types.NodeID]map[snapshotKey]bool

	// blacklists for rejected items
	formatBlacklist    map[uint32]bool
	peerBlacklist      map[types.NodeID]bool
	snapshotBlacklist  map[snapshotKey]bool
	formatRejections   []uint32
	peerRejections     []types.NodeID
	snapshotRejections []snapshotKey
	retainedBytes      int
	peerBytes          map[types.NodeID]int
	associations       int
	keys               map[*snapshot]snapshotKey
	active             *snapshot
	activeKey          snapshotKey
	sequence           uint64
	admitted           map[snapshotKey]uint64
	responses          map[types.NodeID]int
	requestsIssued     int
	pendingPeers       []types.NodeID
	discoveryOffset    int
}

// newSnapshotPool creates a new empty snapshot pool.
func newSnapshotPool() *snapshotPool {
	return &snapshotPool{
		snapshots:         make(map[snapshotKey]*snapshot),
		peerBytes:         make(map[types.NodeID]int),
		keys:              make(map[*snapshot]snapshotKey),
		admitted:          make(map[snapshotKey]uint64),
		responses:         make(map[types.NodeID]int),
		snapshotPeers:     make(map[snapshotKey]map[types.NodeID]types.NodeID),
		versionIndex:      make(map[uint32]map[snapshotKey]bool),
		heightIndex:       make(map[uint64]map[snapshotKey]bool),
		peerIndex:         make(map[types.NodeID]map[snapshotKey]bool),
		formatBlacklist:   make(map[uint32]bool),
		peerBlacklist:     make(map[types.NodeID]bool),
		snapshotBlacklist: make(map[snapshotKey]bool),
	}
}

// Add adds a snapshot to the pool, unless the peer has already sent recentSnapshots
// snapshots. It returns true if this was a new, non-blacklisted snapshot. The
// snapshot height is verified using the light client, and the expected app hash
// is set for the snapshot.
func (p *snapshotPool) Add(peerID types.NodeID, snapshot *snapshot) (bool, error) {
	p.Lock()
	defer p.Unlock()
	size := len(snapshot.Hash) + len(snapshot.Metadata)
	if p.formatBlacklist[snapshot.Version] || p.peerBlacklist[peerID] ||
		len(p.peerIndex[peerID]) >= recentSnapshots || size > maxPeerSnapshotBytes-p.peerBytes[peerID] {
		return false, nil
	}
	key := snapshot.Key()
	if p.snapshotBlacklist[key] || p.peerIndex[peerID][key] {
		return false, nil
	}
	additional := size
	count := 1
	if p.snapshots[key] != nil {
		additional = 0
		count = 0
	}
	if !p.makeRoom(peerID, additional, count) {
		return false, nil
	}
	p.peerBytes[peerID] += size
	p.associations++

	if p.snapshotPeers[key] == nil {
		p.snapshotPeers[key] = make(map[types.NodeID]types.NodeID)
	}
	p.snapshotPeers[key][peerID] = peerID

	if p.peerIndex[peerID] == nil {
		p.peerIndex[peerID] = make(map[snapshotKey]bool)
	}
	p.peerIndex[peerID][key] = true

	if p.snapshots[key] != nil {
		return false, nil
	}
	owned := *snapshot
	if snapshot.Hash != nil {
		owned.Hash = make([]byte, len(snapshot.Hash))
		copy(owned.Hash, snapshot.Hash)
	}
	if snapshot.Metadata != nil {
		owned.Metadata = make([]byte, len(snapshot.Metadata))
		copy(owned.Metadata, snapshot.Metadata)
	}
	snapshot = &owned
	p.snapshots[key] = snapshot
	p.keys[snapshot] = key
	p.retainedBytes += size
	p.sequence++
	p.admitted[key] = p.sequence

	if p.versionIndex[snapshot.Version] == nil {
		p.versionIndex[snapshot.Version] = make(map[snapshotKey]bool)
	}
	p.versionIndex[snapshot.Version][key] = true

	if p.heightIndex[snapshot.Height] == nil {
		p.heightIndex[snapshot.Height] = make(map[snapshotKey]bool)
	}
	p.heightIndex[snapshot.Height][key] = true

	return true, nil
}

// Best returns the "best" currently known snapshot, if any.
func (p *snapshotPool) Best() *snapshot {
	ranked := p.Ranked()
	if len(ranked) == 0 {
		return nil
	}
	return ranked[0]
}

// GetPeer returns a random peer for a snapshot, if any.
func (p *snapshotPool) GetPeer(snapshot *snapshot) types.NodeID {
	peers := p.GetPeers(snapshot)
	if len(peers) == 0 {
		return ""
	}
	return peers[rand.Intn(len(peers))] //nolint:gosec // G404: Use of weak random number generator
}

// GetPeers returns the peers for a snapshot.
func (p *snapshotPool) GetPeers(snapshot *snapshot) []types.NodeID {
	p.Lock()
	defer p.Unlock()
	key, ok := p.keys[snapshot]
	if !ok {
		key = snapshot.Key()
	}

	peers := make([]types.NodeID, 0, len(p.snapshotPeers[key]))
	for _, peer := range p.snapshotPeers[key] {
		peers = append(peers, peer)
	}

	// sort results, for testability (otherwise order is random, so tests randomly fail)
	sort.Slice(peers, func(a int, b int) bool {
		return strings.Compare(string(peers[a]), string(peers[b])) < 0
	})

	return peers
}

// Ranked returns a list of snapshots ranked by preference. The current heuristic is very naïve,
// preferring the snapshot with the greatest height, then greatest format, then greatest number of
// peers. This can be improved quite a lot.
func (p *snapshotPool) Ranked() []*snapshot {
	p.Lock()
	defer p.Unlock()
	return p.ranked()
}

func (p *snapshotPool) ranked() []*snapshot {

	if len(p.snapshots) == 0 {
		return []*snapshot{}
	}

	numPeers := make([]int, 0, len(p.snapshots))
	for key := range p.snapshots {
		numPeers = append(numPeers, len(p.snapshotPeers[key]))
	}
	sort.Ints(numPeers)
	median := len(numPeers) / 2
	if len(numPeers)%2 == 0 {
		median = (numPeers[median-1] + numPeers[median]) / 2
	} else {
		median = numPeers[median]
	}

	commonCandidates := make([]*snapshot, 0, len(p.snapshots)/2)
	uncommonCandidates := make([]*snapshot, 0, len(p.snapshots)/2)
	for key := range p.snapshots {
		if len(p.snapshotPeers[key]) > median {
			commonCandidates = append(commonCandidates, p.snapshots[key])
			continue
		}

		uncommonCandidates = append(uncommonCandidates, p.snapshots[key])
	}

	sort.Slice(commonCandidates, p.sorterFactory(commonCandidates))
	sort.Slice(uncommonCandidates, p.sorterFactory(uncommonCandidates))

	return append(commonCandidates, uncommonCandidates...)
}

func (p *snapshotPool) sorterFactory(candidates []*snapshot) func(int, int) bool {
	return func(i, j int) bool {
		a := candidates[i]
		b := candidates[j]

		switch {
		case a.Height > b.Height:
			return true
		case a.Height < b.Height:
			return false
		case len(p.snapshotPeers[p.keys[a]]) > len(p.snapshotPeers[p.keys[b]]):
			return true
		case a.Version > b.Version:
			return true
		case a.Version < b.Version:
			return false
		default:
			return false
		}
	}
}

// Reject rejects a snapshot and remembers it in the bounded rejection history.
func (p *snapshotPool) Reject(snapshot *snapshot) {
	p.Lock()
	defer p.Unlock()
	key, ok := p.keys[snapshot]
	if !ok {
		key = snapshot.Key()
	}

	rememberRejection(p.snapshotBlacklist, &p.snapshotRejections, key)
	p.removeSnapshot(key)
}

// RejectVersion rejects a snapshot version, retaining a bounded rejection history.
func (p *snapshotPool) RejectVersion(version uint32) {
	p.Lock()
	defer p.Unlock()

	rememberRejection(p.formatBlacklist, &p.formatRejections, version)
	for key := range p.versionIndex[version] {
		p.removeSnapshot(key)
	}
}

// RejectPeer rejects a peer, retaining a bounded rejection history.
func (p *snapshotPool) RejectPeer(peerID types.NodeID) {
	if len(peerID) == 0 {
		return
	}

	p.Lock()
	defer p.Unlock()

	p.removePeer(peerID)
	rememberRejection(p.peerBlacklist, &p.peerRejections, peerID)
}

// RemovePeer removes a peer from the pool, and any snapshots that no longer have peers.
func (p *snapshotPool) RemovePeer(peerID types.NodeID) {
	p.Lock()
	defer p.Unlock()
	p.removePeer(peerID)
}

// removePeer removes a peer. The caller must hold the mutex lock.
func (p *snapshotPool) removePeer(peerID types.NodeID) {
	for key := range p.peerIndex[peerID] {
		delete(p.snapshotPeers[key], peerID)
		p.peerBytes[peerID] -= len(p.snapshots[key].Hash) + len(p.snapshots[key].Metadata)
		p.associations--
		if len(p.snapshotPeers[key]) == 0 {
			p.removeSnapshot(key)
		}
	}

	delete(p.peerIndex, peerID)
	delete(p.peerBytes, peerID)
	delete(p.responses, peerID)
}

// removeSnapshot removes a snapshot. The caller must hold the mutex lock.
func (p *snapshotPool) removeSnapshot(key snapshotKey) {
	snapshot := p.snapshots[key]
	if snapshot == nil {
		return
	}

	if snapshot != p.active {
		p.retainedBytes -= len(snapshot.Hash) + len(snapshot.Metadata)
		delete(p.keys, snapshot)
	}
	delete(p.admitted, key)
	delete(p.snapshots, key)
	delete(p.versionIndex[snapshot.Version], key)
	delete(p.heightIndex[snapshot.Height], key)
	if len(p.versionIndex[snapshot.Version]) == 0 {
		delete(p.versionIndex, snapshot.Version)
	}
	if len(p.heightIndex[snapshot.Height]) == 0 {
		delete(p.heightIndex, snapshot.Height)
	}
	for peerID := range p.snapshotPeers[key] {
		delete(p.peerIndex[peerID], key)
		p.peerBytes[peerID] -= len(snapshot.Hash) + len(snapshot.Metadata)
		p.associations--
		if len(p.peerIndex[peerID]) == 0 {
			delete(p.peerIndex, peerID)
			delete(p.peerBytes, peerID)
		}
	}
	delete(p.snapshotPeers, key)
}

// makeRoom admits a less represented peer by evicting unpinned candidates from richer peers.
// The caller must hold the pool lock.
func (p *snapshotPool) makeRoom(peerID types.NodeID, size, count int) bool {
	for p.retainedBytes+size > maxSnapshotBytes || len(p.keys)+count > maxSnapshots || p.associations+1 > maxSnapshotAssociations {
		var victim snapshotKey
		var found bool
		bytePressure := p.retainedBytes+size > maxSnapshotBytes
		weight := func(peer types.NodeID) int {
			if bytePressure {
				return p.peerBytes[peer]
			}
			return len(p.peerIndex[peer])
		}
		richest := weight(peerID)
		oldest := ^uint64(0)
		for key, candidate := range p.snapshots {
			if candidate == p.active {
				continue
			}
			minimum := int(^uint(0) >> 1)
			for owner := range p.snapshotPeers[key] {
				if weight(owner) < minimum {
					minimum = weight(owner)
				}
			}
			if minimum > richest || (found && minimum == richest && p.admitted[key] < oldest) {
				richest = minimum
				oldest = p.admitted[key]
				victim = key
				found = true
			}
		}
		if !found {
			return false
		}
		p.removeSnapshot(victim)
	}
	return true
}

// TakeBest pins the selected payload until Release, including after its last peer leaves.
func (p *snapshotPool) TakeBest() *snapshot {
	p.Lock()
	defer p.Unlock()
	if p.active != nil {
		return p.active
	}
	ranked := p.ranked()
	if len(ranked) == 0 {
		return nil
	}
	p.active = ranked[0]
	p.activeKey = p.keys[p.active]
	return p.active
}

// Release ends the selected snapshot's lifetime in the pool budget.
func (p *snapshotPool) Release() {
	p.Lock()
	defer p.Unlock()
	if p.active != nil && p.snapshots[p.activeKey] != p.active {
		p.retainedBytes -= len(p.active.Hash) + len(p.active.Metadata)
		delete(p.keys, p.active)
	}
	p.active = nil
}

func rememberRejection[T comparable](entries map[T]bool, order *[]T, value T) {
	if entries[value] {
		return
	}
	if len(*order) == maxSnapshots {
		delete(entries, (*order)[0])
		*order = (*order)[1:]
	}
	entries[value] = true
	*order = append(*order, value)
}

// DiscoveryBatch freezes a bounded peer sweep and rotates batches through it.
func (p *snapshotPool) DiscoveryBatch(peers []types.NodeID) []types.NodeID {
	p.Lock()
	defer p.Unlock()
	if len(p.pendingPeers) == 0 && len(peers) > 0 {
		count := min(len(peers), maxSnapshots)
		for i := 0; i < count; i++ {
			p.pendingPeers = append(p.pendingPeers, peers[(p.discoveryOffset+i)%len(peers)])
		}
		p.discoveryOffset = (p.discoveryOffset + count) % len(peers)
	}
	p.responses = make(map[types.NodeID]int)
	p.requestsIssued = 0
	count := min(len(p.pendingPeers), maxDiscoveryPeers)
	selected := append([]types.NodeID(nil), p.pendingPeers[:count]...)
	p.pendingPeers = p.pendingPeers[count:]
	if len(p.pendingPeers) == 0 {
		p.pendingPeers = nil
	}
	for _, peer := range selected {
		p.requestPeer(peer)
	}
	return selected
}

// DiscoveryPending reports whether the frozen sweep has unrequested peers.
func (p *snapshotPool) DiscoveryPending() bool {
	p.Lock()
	defer p.Unlock()
	return len(p.pendingPeers) > 0
}

// RequestPeer reserves a response allowance without exceeding the batch budget.
func (p *snapshotPool) RequestPeer(peer types.NodeID) bool {
	p.Lock()
	defer p.Unlock()
	return p.requestPeer(peer)
}

func (p *snapshotPool) requestPeer(peer types.NodeID) bool {
	if p.requestsIssued >= maxDiscoveryPeers || p.peerBlacklist[peer] {
		return false
	}
	if _, ok := p.responses[peer]; ok {
		return false
	}
	p.responses[peer] = recentSnapshots
	p.requestsIssued++
	return true
}

// CancelRequest revokes replies after a failed request without replenishing the budget.
func (p *snapshotPool) CancelRequest(peer types.NodeID) {
	p.Lock()
	defer p.Unlock()
	delete(p.responses, peer)
}

// AcceptResponse charges every reply, including duplicates and rejected advertisements.
func (p *snapshotPool) AcceptResponse(peer types.NodeID) bool {
	p.Lock()
	defer p.Unlock()
	if p.responses[peer] == 0 {
		return false
	}
	p.responses[peer]--
	return true
}
