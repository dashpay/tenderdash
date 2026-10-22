package blocksync

import (
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/types"
)

func servicePeers() (*InMemPeerStore, *clockwork.FakeClock) {
	clock := clockwork.NewFakeClock()
	peers := NewInMemPeerStore()
	peers.clock = clock
	peers.Upsert(newPeerData("first", 1, 100))
	peers.Upsert(newPeerData("second", 1, 100))
	return peers, clock
}

func beginServiceRequest(t *testing.T, peers *InMemPeerStore, height int64) (PeerData, *serviceRequest) {
	t.Helper()
	peer, found := peers.reservePeer(height)
	require.True(t, found)
	request := peer.service.request()
	request.Started()
	return peer, request
}

func endServiceRequest(peers *InMemPeerStore, peer PeerData, request *serviceRequest, size int) {
	request.Finished()
	request.complete(size)
	peers.Update(peer.peerID, AddNumPending(-1), ResetFailures())
}

func TestServiceUsefulProgressAndIdleTime(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int
		delay time.Duration
		size  int
	}{
		{"tiny serialized pipeline", 20, time.Second, 100},
		{"large response", 1, 32 * time.Second, 4 * 1024 * 1024},
		{"out of order pipeline", 20, time.Second, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peers, clock := servicePeers()
			requests := make([]*serviceRequest, tc.count)
			var peer PeerData
			for i := range requests {
				peer, requests[i] = beginServiceRequest(t, peers, int64(i+1))
				require.Equal(t, types.NodeID("first"), peer.peerID)
			}
			for i := range requests {
				clock.Advance(tc.delay)
				index := i
				if tc.name == "out of order pipeline" {
					index = len(requests) - 1 - i
				}
				endServiceRequest(peers, peer, requests[index], tc.size)
			}
			for range 3 {
				clock.Advance(time.Hour)
				next, found := peers.FindPeer(30)
				require.True(t, found)
				require.Equal(t, peer.peerID, next.peerID, "idle/apply time must not lower preference")
			}
		})
	}
}

func TestServiceStopsAtReceiptBeforeLocalValidationAndBuffering(t *testing.T) {
	peers, clock := servicePeers()
	peer, request := beginServiceRequest(t, peers, 1)
	clock.Advance(100 * time.Millisecond)
	request.Finished()
	clock.Advance(time.Hour)
	selected, found := peers.FindPeer(2)
	require.True(t, found)
	require.Equal(t, peer.peerID, selected.peerID)
	request.complete(100)
	// Pending is deliberately retained: completed results can wait on the consumer.
	clock.Advance(time.Hour)
	selected, found = peers.FindPeer(2)
	require.True(t, found)
	require.Equal(t, peer.peerID, selected.peerID)
}

func TestServiceUnansweredRequestsAndInvalidReplies(t *testing.T) {
	peers, clock := servicePeers()
	peer, request := beginServiceRequest(t, peers, 1)
	clock.Advance(25 * time.Second)
	selected, found := peers.FindPeer(2)
	require.True(t, found)
	require.Equal(t, types.NodeID("second"), selected.peerID, "unanswered work must consume active time")
	// An invalid response earns no bytes or block credit.
	endServiceRequest(peers, peer, request, 0)
	selected, found = peers.FindPeer(2)
	require.True(t, found)
	require.Equal(t, types.NodeID("second"), selected.peerID)
	require.True(t, peers.HasPeerForHeight(1))
}

func TestServiceLargeCompletionCorrectsProvisionalPenalty(t *testing.T) {
	peers, clock := servicePeers()
	peer, request := beginServiceRequest(t, peers, 1)
	clock.Advance(25 * time.Second)
	selected, _ := peers.FindPeer(2)
	require.Equal(t, types.NodeID("second"), selected.peerID)
	endServiceRequest(peers, peer, request, 4*1024*1024)
	selected, _ = peers.FindPeer(2)
	require.Equal(t, peer.peerID, selected.peerID)
}

func TestServiceProbationProbesAndRecovery(t *testing.T) {
	peers, clock := servicePeers()
	peer, request := beginServiceRequest(t, peers, 1)
	clock.Advance(59 * time.Second)
	endServiceRequest(peers, peer, request, 100)
	peers.Delete("second")
	require.True(t, peers.HasPeerForHeight(1), "cooling source still prevents a false no-source handoff")
	_, found := peers.FindPeer(1)
	require.False(t, found)
	// A status refresh cannot erase the observed service history.
	peers.Upsert(newPeerData(peer.peerID, 1, 200))
	_, found = peers.FindPeer(1)
	require.False(t, found)
	for probe := 0; probe < 2; probe++ {
		clock.Advance(30 * time.Second)
		peer, request = beginServiceRequest(t, peers, 2)
		_, found = peers.reservePeer(3)
		require.False(t, found, "only one recovery probe may be outstanding")
		clock.Advance(100 * time.Millisecond)
		endServiceRequest(peers, peer, request, 100)
		_, found = peers.FindPeer(3)
		require.Equal(t, probe == 1, found, "one fast response cannot erase sustained poor service")
	}
	peer, request = beginServiceRequest(t, peers, 3)
	next, another := beginServiceRequest(t, peers, 4)
	require.Equal(t, peer.peerID, next.peerID, "recovery restores pipelining")
	endServiceRequest(peers, peer, request, 100)
	endServiceRequest(peers, next, another, 100)
}

func TestServiceDueProbeGetsTurnWithHealthyAlternative(t *testing.T) {
	peers, clock := servicePeers()
	peer, request := beginServiceRequest(t, peers, 1)
	clock.Advance(59 * time.Second)
	endServiceRequest(peers, peer, request, 100)
	clock.Advance(30 * time.Second)
	var wg sync.WaitGroup
	selected := make(chan types.NodeID, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			peer, found := peers.reservePeer(2)
			if found {
				selected <- peer.peerID
			}
		}()
	}
	wg.Wait()
	close(selected)
	probes := 0
	for id := range selected {
		if id == peer.peerID {
			probes++
		}
	}
	require.Equal(t, 1, probes, "concurrent acquisition must reserve exactly one due probe")
}

func TestServiceSlowAndFailedProbesKeepSourceRecoverable(t *testing.T) {
	peers, clock := servicePeers()
	peer, request := beginServiceRequest(t, peers, 1)
	clock.Advance(59 * time.Second)
	endServiceRequest(peers, peer, request, 100)
	peers.Delete("second")
	for _, size := range []int{100, 0, 100} {
		clock.Advance(30 * time.Second)
		peer, request = beginServiceRequest(t, peers, 1)
		clock.Advance(59 * time.Second)
		endServiceRequest(peers, peer, request, size)
		require.True(t, peers.HasPeerForHeight(1))
		_, found := peers.FindPeer(1)
		require.False(t, found)
	}
}

func TestServiceReconnectDoesNotInheritLateObservation(t *testing.T) {
	peers, clock := servicePeers()
	old, request := beginServiceRequest(t, peers, 1)
	peers.Delete(old.peerID)
	peers.Upsert(newPeerData(old.peerID, 1, 100))
	peers.Delete("second")
	clock.Advance(59 * time.Second)
	request.Finished()
	request.complete(0)
	current, found := peers.FindPeer(1)
	require.True(t, found)
	require.NotSame(t, old.service, current.service)
}

func TestServiceProbationRemainsServableBelowTip(t *testing.T) {
	clock := clockwork.NewFakeClock()
	syncer := NewSynchronizer(1, nil, nil, WithClock(clock))
	syncer.lastAdvance = clock.Now()
	syncer.AddPeer(newPeerData("only", 1, 1000))
	peer, request := beginServiceRequest(t, syncer.peerStore, 1)
	clock.Advance(59 * time.Second)
	endServiceRequest(syncer.peerStore, peer, request, 100)
	_, found := syncer.peerStore.FindPeer(1)
	require.False(t, found, "peer is cooling")
	_, _, servable, _, hasPeers, maxServable := syncer.stallSnapshot()
	require.True(t, servable)
	require.True(t, hasPeers)
	require.EqualValues(t, 1000, maxServable)
}

func TestServiceSavedCreditCannotHideNewSlowService(t *testing.T) {
	peers, clock := servicePeers()
	for range 100 {
		peer, request := beginServiceRequest(t, peers, 1)
		endServiceRequest(peers, peer, request, 4*1024*1024)
	}
	peer, request := beginServiceRequest(t, peers, 2)
	clock.Advance(59 * time.Second)
	endServiceRequest(peers, peer, request, 100)
	selected, found := peers.FindPeer(3)
	require.True(t, found)
	require.Equal(t, types.NodeID("second"), selected.peerID)
}
