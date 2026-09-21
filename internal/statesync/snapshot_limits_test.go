package statesync

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/types"
)

func TestSnapshotPoolRetainedPayloadBudget(t *testing.T) {
	p := newSnapshotPool()
	for i := 0; i < 20; i++ {
		_, err := p.Add(types.NodeID(fmt.Sprint(i)), &snapshot{Height: uint64(i + 1), Version: 1, Hash: []byte{1}, Metadata: make([]byte, 3_999_900)})
		require.NoError(t, err)
	}
	total := 0
	for _, s := range p.snapshots {
		total += len(s.Hash) + len(s.Metadata)
	}
	require.LessOrEqual(t, total, 64*1024*1024, "retained payload must be bounded independently of peer count")
}

func TestSnapshotPoolOwnsPayload(t *testing.T) {
	p := newSnapshotPool()
	source := &snapshot{Height: 1, Version: 1, Hash: []byte{1}, Metadata: make([]byte, 1, 8*1024*1024)}
	source.Metadata[0] = 7
	added, err := p.Add("peer", source)
	require.NoError(t, err)
	require.True(t, added)
	source.Hash[0] = 2
	source.Metadata[0] = 9
	retained := p.Best()
	require.Equal(t, byte(1), retained.Hash[0])
	require.Equal(t, byte(7), retained.Metadata[0])
	require.Equal(t, len(retained.Metadata), cap(retained.Metadata), "a tiny view must not retain its huge backing allocation")
}

func TestSnapshotPoolSharedAccountingAndRelease(t *testing.T) {
	for _, remove := range []string{"peer", "snapshot", "version", "rejected-peer"} {
		t.Run(remove, func(t *testing.T) {
			p := newSnapshotPool()
			input := &snapshot{Height: 1, Version: 1, Hash: []byte{1}, Metadata: []byte{2, 3}}
			added, err := p.Add("a", input)
			require.NoError(t, err)
			require.True(t, added)
			added, err = p.Add("b", input)
			require.NoError(t, err)
			require.False(t, added)
			require.Equal(t, 3, p.retainedBytes)
			require.Equal(t, 3, p.peerBytes["a"])
			require.Equal(t, 3, p.peerBytes["b"])
			require.Equal(t, 2, p.associations)
			active := p.TakeBest()
			switch remove {
			case "peer":
				p.RemovePeer("a")
				p.RemovePeer("b")
			case "snapshot":
				p.Reject(active)
			case "version":
				p.RejectVersion(1)
			case "rejected-peer":
				p.RejectPeer("a")
				p.RejectPeer("b")
			}
			require.Empty(t, p.snapshots)
			require.Equal(t, 3, p.retainedBytes, "active payload remains charged after removal")
			require.Equal(t, []byte{2, 3}, active.Metadata)
			require.Zero(t, p.associations)
			require.Empty(t, p.peerBytes)
			require.Empty(t, p.peerIndex)
			require.Empty(t, p.heightIndex)
			require.Empty(t, p.versionIndex)
			p.Release()
			p.Release()
			require.Zero(t, p.retainedBytes)
			require.Empty(t, p.keys)
		})
	}
}

func TestSnapshotPoolPressureProtectsActiveAndAdmitsNewPeer(t *testing.T) {
	p := newSnapshotPool()
	for i := 0; i < 16; i++ {
		peer := types.NodeID("rich-a")
		if i >= 10 {
			peer = "rich-b"
		}
		added, err := p.Add(peer, &snapshot{Height: uint64(i + 1), Version: 1, Hash: []byte{1}, Metadata: make([]byte, 3_999_900)})
		require.NoError(t, err)
		require.True(t, added)
	}
	active := p.TakeBest()
	added, err := p.Add("later-honest-peer", &snapshot{Height: 1, Version: 1, Hash: []byte{2}, Metadata: make([]byte, 3_999_900)})
	require.NoError(t, err)
	require.True(t, added, "a less represented peer must get an admission opportunity at capacity")
	require.Same(t, active, p.snapshots[p.activeKey])
	require.LessOrEqual(t, p.retainedBytes, maxSnapshotBytes)
	require.Less(t, p.peerBytes["rich-a"], 10*3_999_901)
	p.Release()
}

func TestSnapshotPoolPeerPayloadLimit(t *testing.T) {
	p := newSnapshotPool()
	for i := 0; i < 10; i++ {
		added, err := p.Add("a", &snapshot{Height: uint64(i + 1), Hash: []byte{1}, Metadata: make([]byte, 3_999_999)})
		require.NoError(t, err)
		require.True(t, added)
	}
	require.Equal(t, 40_000_000, p.peerBytes["a"])
	added, err := p.Add("a", &snapshot{Height: 11, Hash: []byte{1}})
	require.NoError(t, err)
	require.False(t, added)
	p.RemovePeer("a")
	require.Zero(t, p.retainedBytes)
}

func TestSnapshotPoolBoundedBookkeeping(t *testing.T) {
	p := newSnapshotPool()
	for i := 0; i < maxSnapshots+20; i++ {
		_, err := p.Add(types.NodeID(fmt.Sprint(i)), &snapshot{Height: uint64(i + 1), Version: uint32(i), Hash: []byte{1}})
		require.NoError(t, err)
	}
	require.Len(t, p.snapshots, maxSnapshots)
	require.LessOrEqual(t, len(p.heightIndex), maxSnapshots)
	require.LessOrEqual(t, len(p.versionIndex), maxSnapshots)
	require.LessOrEqual(t, len(p.peerIndex), maxSnapshotAssociations)
	for i := 0; i < maxSnapshots+20; i++ {
		p.Reject(&snapshot{Height: uint64(i + 1), Version: uint32(i), Hash: []byte{1}})
		p.RejectVersion(uint32(i))
		p.RejectPeer(types.NodeID(fmt.Sprint(i)))
	}
	require.Len(t, p.snapshotBlacklist, maxSnapshots)
	require.Len(t, p.formatBlacklist, maxSnapshots)
	require.Len(t, p.peerBlacklist, maxSnapshots)
	require.Empty(t, p.snapshots)
	require.Empty(t, p.peerIndex)
	require.Empty(t, p.keys)
}

func TestSnapshotDiscoveryAllowancesAndRotation(t *testing.T) {
	p := newSnapshotPool()
	peers := make([]types.NodeID, maxDiscoveryPeers+1, maxDiscoveryPeers+2)
	for i := range peers {
		peers[i] = types.NodeID(fmt.Sprint(i))
	}
	first := p.DiscoveryBatch(peers)
	require.Len(t, first, maxDiscoveryPeers)
	require.True(t, p.DiscoveryPending())
	require.False(t, p.RequestPeer("late-join"))
	require.False(t, p.AcceptResponse(peers[maxDiscoveryPeers]))
	for i := 0; i < recentSnapshots; i++ {
		require.True(t, p.AcceptResponse(first[0]))
	}
	require.False(t, p.AcceptResponse(first[0]))
	p.RemovePeer(first[0])
	require.False(t, p.RequestPeer("churn"), "disconnect must not renew the round's CPU allowance")
	next := p.DiscoveryBatch(append(peers, "churn"))
	require.Equal(t, peers[maxDiscoveryPeers:], next, "membership stays frozen during a sweep")
	require.False(t, p.DiscoveryPending())
	require.True(t, p.AcceptResponse(next[0]))
	require.False(t, p.AcceptResponse(first[1]), "old batch replies are ignored without a peer penalty")
	require.True(t, p.RequestPeer("late-join"))
	p.CancelRequest("late-join")
	require.False(t, p.AcceptResponse("late-join"))
}

func TestSnapshotPoolConcurrentAdmissionAndRemoval(t *testing.T) {
	p := newSnapshotPool()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			peer := types.NodeID(fmt.Sprint(i))
			for j := 0; j < 20; j++ {
				_, err := p.Add(peer, &snapshot{Height: uint64(j + 1), Hash: []byte{byte(j)}, Metadata: []byte{1}})
				assert.NoError(t, err)
				p.Ranked()
				p.RemovePeer(peer)
			}
		}(i)
	}
	wg.Wait()
	require.Zero(t, p.retainedBytes)
	require.Zero(t, p.associations)
	require.Empty(t, p.peerBytes)
	require.Empty(t, p.snapshots)
}

func TestSnapshotPoolAssociationBound(t *testing.T) {
	p := newSnapshotPool()
	input := &snapshot{Height: 1, Version: 1, Hash: []byte{1}}
	for i := 0; i < maxSnapshotAssociations+1; i++ {
		_, err := p.Add(types.NodeID(fmt.Sprint(i)), input)
		require.NoError(t, err)
		require.LessOrEqual(t, p.associations, maxSnapshotAssociations)
	}
	require.Equal(t, 1, p.retainedBytes)
	require.LessOrEqual(t, len(p.peerIndex), maxSnapshotAssociations)
	for peer := range p.peerIndex {
		p.RemovePeer(peer)
	}
	require.Zero(t, p.retainedBytes)
	require.Zero(t, p.associations)
}

func TestSnapshotPoolDetachedActiveCountsTowardObjectLimit(t *testing.T) {
	p := newSnapshotPool()
	_, err := p.Add("active-peer", &snapshot{Height: 1, Hash: []byte{1}})
	require.NoError(t, err)
	active := p.TakeBest()
	p.RemovePeer("active-peer")
	for i := 0; i < maxSnapshots; i++ {
		_, err = p.Add(types.NodeID(fmt.Sprint(i)), &snapshot{Height: uint64(i + 2), Hash: []byte{1}})
		require.NoError(t, err)
	}
	require.Same(t, active, p.active)
	require.Len(t, p.keys, maxSnapshots, "the detached active object still consumes a slot")
	require.Len(t, p.snapshots, maxSnapshots-1)
	p.Release()
	require.Len(t, p.keys, maxSnapshots-1)
}

func TestSnapshotPoolReadmissionWhileDetachedActive(t *testing.T) {
	p := newSnapshotPool()
	input := &snapshot{Height: 1, Version: 1, Hash: []byte{1}, Metadata: []byte{2, 3}}
	_, err := p.Add("original-peer", input)
	require.NoError(t, err)
	active := p.TakeBest()
	p.RemovePeer("original-peer")
	require.Empty(t, p.peerBytes)
	require.Equal(t, 3, p.retainedBytes)
	added, err := p.Add("returning-peer", input)
	require.NoError(t, err)
	require.True(t, added)
	require.NotSame(t, active, p.Best())
	require.Equal(t, 6, p.retainedBytes, "both owned payload copies remain live")
	require.Equal(t, 3, p.peerBytes["returning-peer"])
	p.Release()
	require.Equal(t, 3, p.retainedBytes)
	require.Len(t, p.keys, 1)
	p.RemovePeer("returning-peer")
	require.Zero(t, p.retainedBytes)
	require.Empty(t, p.keys)
}

// TestSnapshotPoolRejectsEmptyHash asserts that a snapshot without a hash is never admitted,
// because it cannot be restored and would otherwise rank first and abort state sync.
func TestSnapshotPoolRejectsEmptyHash(t *testing.T) {
	p := newSnapshotPool()
	for _, hash := range [][]byte{nil, {}} {
		added, err := p.Add("peer", &snapshot{Height: ^uint64(0), Version: 1, Hash: hash})
		require.NoError(t, err)
		require.False(t, added)
	}
	require.Empty(t, p.snapshots)
	require.Zero(t, p.retainedBytes)
	require.Nil(t, p.TakeBest())
}

// TestSnapshotDiscoveryBatchSkipsRejectedPeers asserts that rejected peers never consume
// discovery batch slots or discovery intervals.
func TestSnapshotDiscoveryBatchSkipsRejectedPeers(t *testing.T) {
	p := newSnapshotPool()
	peers := make([]types.NodeID, maxDiscoveryPeers+4)
	for i := range peers {
		peers[i] = types.NodeID(fmt.Sprint(i))
	}
	for _, peer := range peers[:4] {
		p.RejectPeer(peer)
	}

	batch := p.DiscoveryBatch(peers)
	require.Len(t, batch, maxDiscoveryPeers)
	for _, peer := range peers[:4] {
		require.NotContains(t, batch, peer)
	}
	require.False(t, p.DiscoveryPending(), "rejected peers must not extend the sweep")
}
