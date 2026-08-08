package blocksync

import (
	"fmt"
	"sync"
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

// TestNumPendingNeverGoesNegative checks that the pending request count stops at
// zero however the caller sequences its updates. The count gates both the per-peer
// request limit and the slow-peer check, and a negative value passes the first
// unconditionally while never matching the second, so a peer that has drifted
// below zero is handed unlimited concurrent requests and can never be evicted for
// being slow.
func TestNumPendingNeverGoesNegative(t *testing.T) {
	peerID := types.NodeID("peer1")
	inmem := NewInMemPeerStore(newPeerData(peerID, 1, 100))

	inmem.Update(peerID, AddNumPending(1))
	// more completions than requests issued: both the success and the failure path
	// account for a request, and a caller that accounts for the same one twice must
	// not leave the count below zero
	inmem.Update(peerID, AddNumPending(-1))
	inmem.AddFailure(peerID, 10)
	inmem.Update(peerID, AddNumPending(-1))

	peer, found := inmem.Get(peerID)
	require.True(t, found)
	require.EqualValues(t, 0, peer.numPending)
}

// TestUpsertRacingFirstInsertKeepsIssuedRequests checks that recording a peer's
// advertised range never overwrites state stored concurrently under the same peer.
// The p2p consumer goroutine makes a peer known while the job producer starts
// issuing requests against it immediately, so a lookup and an insert that are not
// one operation lose exactly the accounting a status refresh must preserve.
func TestUpsertRacingFirstInsertKeepsIssuedRequests(t *testing.T) {
	const (
		refreshers = 8
		trials     = 200
	)
	peerID := types.NodeID("peer1")
	for trial := 0; trial < trials; trial++ {
		inmem := NewInMemPeerStore()
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(refreshers)
		for i := 0; i < refreshers; i++ {
			go func(i int) {
				defer wg.Done()
				<-start
				inmem.Upsert(newPeerData(peerID, 1, int64(100+i)))
				inmem.Update(peerID, AddNumPending(1))
			}(i)
		}
		close(start)
		wg.Wait()

		peer, found := inmem.Get(peerID)
		require.True(t, found)
		require.EqualValues(t, refreshers, peer.numPending,
			"status refreshes racing the peer's first insert lost issued requests (trial %d)", trial)
	}
}

// TestMaxHeightMatchesStoredPeers checks that the highest advertised height is one
// a peer we still hold actually claims to serve. It decides whether the node is
// caught up and whether a stalled sync gives up, so a height above every peer's
// leaves the node waiting in block sync for a block nobody can send it.
func TestMaxHeightMatchesStoredPeers(t *testing.T) {
	const (
		trials  = 200
		rounds  = 50
		lowPeer = int64(10)
	)
	peerID := types.NodeID("peer1")
	otherID := types.NodeID("peer2")
	for trial := 0; trial < trials; trial++ {
		inmem := NewInMemPeerStore(newPeerData(otherID, 1, lowPeer))
		var wg sync.WaitGroup
		wg.Add(3)
		// the same peer re-advertising a growing range, re-advertising a lower one,
		// and being dropped - the three ways its stored height moves
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				inmem.Upsert(newPeerData(peerID, 1, int64(100+i)))
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				inmem.Upsert(newPeerData(peerID, 1, 20))
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				inmem.Delete(peerID)
			}
		}()
		wg.Wait()

		var want int64
		for _, peer := range inmem.All() {
			want = max(want, peer.height)
		}
		require.Equal(t, want, inmem.MaxHeight(),
			"reported a height no stored peer advertises (trial %d)", trial)
	}
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
