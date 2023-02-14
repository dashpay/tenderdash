package blocksync

import (
	"fmt"
	"testing"
	"time"

	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/require"

	"github.com/tendermint/tendermint/internal/libs/flowrate"
	"github.com/tendermint/tendermint/types"
)

func TestInMemPeerStoreBasicOperations(t *testing.T) {
	peerID := types.NodeID("peer id")
	peer := newPeerData(peerID, 1, 100)
	inmem := NewInMemPeerStore()
	_, found := inmem.Get(peerID)
	require.False(t, found)

	// add a peer to store
	inmem.Put(peer)
	foundPeer, found := inmem.Get(peerID)
	require.True(t, found)
	require.Equal(t, peer, foundPeer)

	// update a peer data
	updatedPeer := newPeerData(peerID, 100, 200)
	inmem.Put(updatedPeer)
	foundPeer, found = inmem.Get(peerID)
	require.True(t, found)
	require.Equal(t, updatedPeer.height, foundPeer.height)
	require.Equal(t, updatedPeer.base, foundPeer.base)

	inmem.PeerUpdate(peerID, AddNumPending(1))
	require.Equal(t, int32(0), foundPeer.numPending)
	foundPeer, found = inmem.Get(peerID)
	require.Equal(t, int32(1), foundPeer.numPending)

	require.Equal(t, 1, inmem.Len())
	require.False(t, inmem.IsZero())

	inmem.Remove(peerID)
	require.Equal(t, 0, inmem.Len())
	require.True(t, inmem.IsZero())
}

func TestInMemPeerStoreFindPeer(t *testing.T) {
	fakeClock := clock.NewMock()
	flowrate.Now = func() time.Time {
		return fakeClock.Now()
	}
	defer func() {
		flowrate.Now = flowrate.TimeNow
	}()
	monitor := flowrate.New(time.Now(), 1*time.Second, 10*time.Second)
	fakeClock.Add(5 * time.Second)
	monitor.Update(10000)
	peers := []PeerData{
		newPeerData("peer 1", 1, 100),
		newPeerData("peer 2", 50, 100),
		newPeerData("peer 3", 101, 200),
		// timeout peers
		newPeerData("peer 4", 1, 100),
	}
	peers[3].numPending = 1
	peers[3].recvMonitor = monitor
	inmem := NewInMemPeerStore(peers...)
	testCases := []struct {
		peers  []PeerData
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
			foundPeer, found := inmem.FindPeer(tc.height)
			if len(tc.wants) == 0 {
				require.False(t, found)
				return
			}
			require.Contains(t, tc.wants, foundPeer.peerID)
		})
	}
	timedoutPeers := inmem.FindTimedoutPeers()
	require.Len(t, timedoutPeers, 1)
	require.Equal(t, peers[3].peerID, timedoutPeers[0].peerID)
}
