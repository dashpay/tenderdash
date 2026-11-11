package blocksync

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/p2p/client"
	"github.com/dashpay/tenderdash/internal/p2p/client/mocks"
	"github.com/dashpay/tenderdash/libs/promise"
	"github.com/dashpay/tenderdash/libs/workerpool"
	bcproto "github.com/dashpay/tenderdash/proto/tendermint/blocksync"
	"github.com/dashpay/tenderdash/types"
)

func reservedIDs(peers []PeerData) []types.NodeID {
	ids := make([]types.NodeID, 0, len(peers))
	for _, peer := range peers {
		ids = append(ids, peer.peerID)
	}
	return ids
}

// TestReserveNextBlockPeers checks who is asked for the height block sync waits
// to apply, and when: nobody until its block is serviceGrace overdue, then every
// peer that advertises it, each once and a second after it is first seen
// advertising it, until the block arrives or the height is applied.
func TestReserveNextBlockPeers(t *testing.T) {
	const height = int64(100)
	clock := clockwork.NewFakeClock()
	sync := NewSynchronizer(height, nil, nil, WithClock(clock), WithWorkerPool(workerpool.New(0)))
	sync.lastAdvance = clock.Now()
	sync.AddPeer(newPeerData("first", 1, height))
	sync.AddPeer(newPeerData("second", 1, height+1000))
	sync.AddPeer(newPeerData("above", height+1, height+1000))
	busy := newPeerData("busy", 1, height)
	busy.numPending = maxPendingRequestsPerPeer
	sync.AddPeer(busy)
	next := func() []types.NodeID {
		clock.Advance(time.Second)
		reservedFor, peers := sync.reserveNextBlockPeers()
		applying, _ := sync.GetStatus()
		require.Equal(t, applying, reservedFor)
		return reservedIDs(peers)
	}

	clock.Advance(serviceGrace - 2*time.Second)
	require.Empty(t, next(), "the block is not overdue yet")
	require.Empty(t, next(), "the block is not overdue yet")
	require.Empty(t, next(), "peers advertising the height are given a second first")
	require.ElementsMatch(t, []types.NodeID{"first", "second"}, next())
	for _, peerID := range []types.NodeID{"first", "second"} {
		peer, _ := sync.peerStore.Get(peerID)
		require.EqualValues(t, 1, peer.numPending, "the request counts against the peer's limit")
	}
	require.Empty(t, next(), "each peer is asked once")

	// a peer that left is not kept on record
	sync.RemovePeer("first")
	require.NotContains(t, sync.asked, types.NodeID("first"))
	sync.AddPeer(newPeerData("first", 1, height))
	require.Empty(t, next())
	require.ElementsMatch(t, []types.NodeID{"first"}, next())

	// a peer that starts advertising the height, and one that gets free
	sync.AddPeer(newPeerData("above", 1, height+1000))
	sync.peerStore.Update("busy", AddNumPending(-1))
	require.ElementsMatch(t, []types.NodeID{"busy"}, next())
	require.ElementsMatch(t, []types.NodeID{"above"}, next())

	// nobody else is asked once the block is here
	sync.AddPeer(newPeerData("late", 1, height))
	require.NoError(t, sync.addBlock(BlockResponse{
		PeerID: "first",
		Block:  &types.Block{Header: types.Header{Height: height}},
	}))
	require.Empty(t, next())
	require.Empty(t, next())

	// the next height starts over
	sync.advance(clock.Now(), defaultPrecision)
	clock.Advance(serviceGrace - time.Second)
	require.Empty(t, next(), "the next block is not overdue yet")
	require.Empty(t, next())
	require.ElementsMatch(t, []types.NodeID{"second", "above"}, next())
}

// TestNextBlockIsFetchedWhileTheProducerWaits checks that the height block sync
// waits to apply is requested again after its only request failed, although the
// job producer is waiting for a peer to serve a higher height that only a peer
// with no room for another request reports.
func TestNextBlockIsFetchedWhileTheProducerWaits(t *testing.T) {
	const height = int64(100)
	var asked atomic.Int32
	// fail is closed once the producer has moved on to the next height
	fail := make(chan struct{})
	blockClient := mocks.NewBlockClient(t)
	blockClient.
		On("GetBlock", mock.Anything, height, types.NodeID("idle peer")).
		Return(func(context.Context, int64, types.NodeID) *promise.Promise[*bcproto.BlockResponse] {
			asked.Add(1)
			return promise.New(func(_ func(*bcproto.BlockResponse), reject func(error)) {
				<-fail
				reject(client.ErrPeerNotResponded)
			})
		}, nil)
	clock := clockwork.NewFakeClock()
	sync := NewSynchronizer(height, blockClient, nil, WithClock(clock), WithWorkerPool(workerpool.New(2)))
	sync.AddPeer(newPeerData("idle peer", 1, height))
	busy := newPeerData("busy claimant", height+1, maxPlausiblePeerHeight)
	busy.numPending = maxPendingRequestsPerPeer
	sync.AddPeer(busy)

	require.NoError(t, sync.Start(context.Background()))
	defer sync.Stop()
	require.Eventually(t, func() bool { return asked.Load() == 1 }, 5*time.Second, time.Millisecond,
		"the producer requests the height first")
	time.Sleep(50 * time.Millisecond)
	close(fail)

	require.Eventually(t, func() bool {
		clock.Advance(time.Second)
		return asked.Load() >= 2
	}, 10*time.Second, 5*time.Millisecond,
		"height %d was not requested again while the producer waits for a peer for height %d", height, height+1)
}
