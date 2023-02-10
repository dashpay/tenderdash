package blocksync

import (
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tendermint/tendermint/types"
)

func TestInMemPeerStoreBasicOperations(t *testing.T) {
	peerID := types.NodeID("peer id")
	peer := newPeerData(peerID, 1, 100)
	inmem := NewInMemPeerStore()
	foundPeer, found := inmem.Get(peerID)
	require.False(t, found)
	require.Nil(t, foundPeer)

	// add a peer to store
	inmem.Put(peer)
	foundPeer, found = inmem.Get(peerID)
	require.True(t, found)
	require.Equal(t, peer, foundPeer)

	// update a peer data
	updatedPeer := newPeerData(peerID, 100, 200)
	inmem.Update(updatedPeer)
	foundPeer, found = inmem.Get(peerID)
	require.True(t, found)
	require.Equal(t, updatedPeer.height, foundPeer.height)
	require.Equal(t, updatedPeer.base, foundPeer.base)
	require.Equal(t, peer.height, foundPeer.height)
	require.Equal(t, peer.base, foundPeer.base)

	require.Equal(t, 1, inmem.Len())
	require.False(t, inmem.IsZero())

	inmem.Remove(peerID)
	require.Equal(t, 0, inmem.Len())
	require.True(t, inmem.IsZero())
}

func TestInMemPeerStoreFindPeer(t *testing.T) {
	peers := []*PeerData{
		newPeerData("peer 1", 1, 100),
		newPeerData("peer 2", 50, 100),
		newPeerData("peer 3", 101, 200),
		// timeout peers
		newPeerData("peer 4", 1, 100),
		newPeerData("peer 5", 1, 100),
		newPeerData("peer 6", 201, 300),
	}
	peers[3].didTimeout.Store(true)
	peers[4].didTimeout.Store(true)
	peers[5].didTimeout.Store(true)
	wantTimedoutPeers := []*PeerData{peers[3], peers[4], peers[5]}
	inmem := NewInMemPeerStore(peers...)
	testCases := []struct {
		peers  []*PeerData
		height int64
		wants  []types.NodeID
	}{
		{
			peers:  peers,
			height: 1,
			wants:  []types.NodeID{peers[0].peerID},
		},
		{
			peers:  peers,
			height: 49,
			wants:  []types.NodeID{peers[0].peerID},
		},
		{
			peers:  peers,
			height: 50,
			wants:  []types.NodeID{peers[0].peerID, peers[1].peerID},
		},
		{
			peers:  peers,
			height: 100,
			wants:  []types.NodeID{peers[0].peerID, peers[1].peerID},
		},
		{
			peers:  peers,
			height: 101,
			wants:  []types.NodeID{peers[2].peerID},
		},
		{
			peers:  peers,
			height: 201,
			wants:  []types.NodeID{},
		},
	}
	// FindPeer an available peer
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			foundPeer := inmem.FindPeer(tc.height)
			if len(tc.wants) == 0 {
				require.Nil(t, foundPeer)
				return
			}
			require.Contains(t, tc.wants, foundPeer.peerID)
		})
	}
	timedoutPeers := inmem.FindTimedoutPeers()
	sort.Slice(timedoutPeers, func(i, j int) bool {
		return timedoutPeers[i].peerID < timedoutPeers[j].peerID
	})
	require.Equal(t, wantTimedoutPeers, timedoutPeers)
}
