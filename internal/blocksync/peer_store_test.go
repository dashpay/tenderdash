package blocksync

import (
	"fmt"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/libs/flowrate"
	"github.com/dashpay/tenderdash/types"
)

func TestInMemPeerStoreBasicOperations(t *testing.T) {
	peerID := types.NodeID("peer id")
	peer := newPeerData(peerID, 1, 100)
	inmem := NewInMemPeerStore()
	_, found := inmem.Get(peerID)
	require.False(t, found)

	// add a peer to store
	inmem.Put(peer.peerID, peer)
	foundPeer, found := inmem.Get(peerID)
	require.True(t, found)
	require.Equal(t, peer, foundPeer)

	// update a peer data
	updatedPeer := newPeerData(peerID, 100, 200)
	inmem.Put(updatedPeer.peerID, updatedPeer)
	foundPeer, found = inmem.Get(peerID)
	require.True(t, found)
	require.Equal(t, updatedPeer.height, foundPeer.height)
	require.Equal(t, updatedPeer.base, foundPeer.base)

	inmem.Update(peerID, AddNumPending(1))
	require.Equal(t, int32(0), foundPeer.numPending)
	foundPeer, found = inmem.Get(peerID)
	require.True(t, found)
	require.Equal(t, int32(1), foundPeer.numPending)

	require.Equal(t, 1, inmem.Len())
	require.False(t, inmem.IsZero())

	inmem.Delete(peerID)
	require.Equal(t, 0, inmem.Len())
	require.True(t, inmem.IsZero())
}

func TestInMemPeerStoreFindPeer(t *testing.T) {
	fakeClock := clockwork.NewFakeClock()
	flowrate.Now = func() time.Time {
		return fakeClock.Now()
	}
	defer func() {
		flowrate.Now = flowrate.TimeNow
	}()
	monitor := flowrate.New(time.Now(), 1*time.Second, 10*time.Second)
	fakeClock.Advance(5 * time.Second)
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

// TestAddFailure checks that a peer is only reported once it has failed
// maxFailures requests in a row, and that a success in between clears the run.
func TestAddFailure(t *testing.T) {
	const maxFailures int32 = 3
	peerID := types.NodeID("peer1")
	inmem := NewInMemPeerStore(newPeerData(peerID, 1, 100))

	// requests below the threshold keep the peer
	for i := int32(1); i < maxFailures; i++ {
		require.False(t, inmem.AddFailure(peerID, maxFailures),
			"peer must survive failure %d of %d", i, maxFailures)
	}
	require.True(t, inmem.AddFailure(peerID, maxFailures),
		"peer must be reported on the last failure")

	// a success clears the run, so the next failure starts over
	inmem.Update(peerID, ResetFailures())
	require.False(t, inmem.AddFailure(peerID, maxFailures),
		"a success must clear the failure count")

	// an unknown peer is never reported, so failures arriving after a peer was
	// already removed do not report it a second time
	require.False(t, inmem.AddFailure(types.NodeID("nope"), maxFailures))
}

// TestAddFailureClearsPending checks that a failed request stops being counted
// as pending; otherwise the peer's pending count only grows and it stops being
// selected once it reaches maxPendingRequestsPerPeer.
func TestAddFailureClearsPending(t *testing.T) {
	peerID := types.NodeID("peer1")
	inmem := NewInMemPeerStore(newPeerData(peerID, 1, 100))

	inmem.Update(peerID, AddNumPending(2))
	inmem.AddFailure(peerID, 10)
	inmem.AddFailure(peerID, 10)

	peer, found := inmem.Get(peerID)
	require.True(t, found)
	require.EqualValues(t, 0, peer.numPending)
	require.EqualValues(t, 2, peer.numFailures)
}
