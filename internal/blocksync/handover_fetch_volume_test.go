package blocksync

import (
	"fmt"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/dashpay/tenderdash/internal/p2p/client"
	"github.com/dashpay/tenderdash/types"
)

// linkRun drives tipRun for a node far behind whose peers each send one block
// at a time over their connection, taking perBlock for each, as a peer serving
// its response queue over one rate-limited connection does. Every request is
// answered with the block, in the order the peer was asked.
//
// With downlink set, the node receives at most that many blocks a second over
// all its connections, shared between the peers in turn, each peer sending at
// most one a second, and a request not answered within the request deadline
// fails as it does in the block client.
type linkRun struct {
	*tipRun
	perBlock time.Duration
	downlink int
	// busyUntil is when a peer finishes sending what it was asked for so far.
	busyUntil map[types.NodeID]time.Time
	scheduled map[*tipRequest]bool
	// slowest is the longest a request took to be answered.
	slowest time.Duration

	// peers is the order the downlink is shared in; queues and askedAt hold
	// what each peer has still to send and when it was asked.
	peers    []types.NodeID
	queues   map[types.NodeID][]*tipRequest
	askedAt  map[*tipRequest]time.Time
	turn     int
	timeouts int
}

// never is when a response waiting for the downlink is due.
var never = time.Unix(1<<40, 0)

func (l *linkRun) step(time.Duration) {
	if l.downlink > 0 {
		l.sharedStep()
		return
	}
	now := l.h.clock.Now()
	for {
		consumed := l.consume(now)
		produced := l.produce(now)
		for _, req := range l.inFlight {
			if l.scheduled[req] {
				continue
			}
			l.scheduled[req] = true
			start := now
			if busy := l.busyUntil[req.peer.peerID]; busy.After(start) {
				start = busy
			}
			req.endAt = start.Add(l.perBlock)
			l.busyUntil[req.peer.peerID] = req.endAt
			l.slowest = max(l.slowest, req.endAt.Sub(now))
		}
		if !consumed && !produced {
			return
		}
	}
}

// TestBlockSyncFarBehindFetchesEachBlockOnce checks how many block responses a
// node far behind downloads for the blocks it applies. Twelve peers hold every
// block and none fails or delays a request beyond its place in the peer's
// queue. When a peer needs a second to send a block, a full queue of twenty
// takes twenty seconds, well inside the 60-second request deadline; the node
// must still fetch each block about once, as it does when blocks arrive at
// once.
func (suite *SynchronizerTestSuite) TestBlockSyncFarBehindFetchesEachBlockOnce() {
	const (
		height = int64(100)
		blocks = int64(600)
		peers  = 12
	)
	for _, tc := range []struct {
		name     string
		perBlock time.Duration
	}{
		{name: "blocks arrive at once"},
		{name: "a peer sends a block a second", perBlock: time.Second},
	} {
		suite.Run(tc.name, func() {
			suite.client.On("Send", mock.Anything, mock.AnythingOfType("p2p.PeerError")).Maybe().Return(nil)
			h := suite.newWaitForSyncHarness(height)
			defer h.cancel()
			// the node is re-syncing history committed long ago
			h.setLastBlockTime(h.clock.Now().Add(-365 * 24 * time.Hour))
			l := &linkRun{
				tipRun: &tipRun{
					h:          h,
					blockTimes: map[int64]time.Time{},
					suppliedBy: map[types.NodeID]int{},
				},
				perBlock:  tc.perBlock,
				busyUntil: map[types.NodeID]time.Time{},
				scheduled: map[*tipRequest]bool{},
			}
			for i := 0; i < peers; i++ {
				h.sync.AddPeer(newPeerData(types.NodeID(fmt.Sprintf("peer %d", i)), 1, height+blocks-1))
			}

			elapsed, caughtUp, ended := h.run(30*time.Minute, l.step)
			suite.Require().True(ended, "not caught up after %s: %d blocks applied", elapsed, l.applied)
			suite.Require().True(caughtUp)
			// the handover comes with the last block still to apply
			suite.Require().GreaterOrEqual(int64(l.applied), blocks-1)
			suite.T().Logf("%d blocks applied in %s, %d block responses downloaded, slowest answer %s",
				l.applied, elapsed, l.blocksSent, l.slowest)
			suite.Require().LessOrEqual(l.blocksSent, 2*l.applied,
				"%d blocks applied in %s, %d block responses downloaded from %d peers, slowest answer %s",
				l.applied, elapsed, l.blocksSent, peers, l.slowest)
		})
	}
}

// sharedStep is step for a node with a limited downlink.
func (l *linkRun) sharedStep() {
	now := l.h.clock.Now()
	// requests past the deadline fail
	for _, peerID := range l.peers {
		var kept []*tipRequest
		for _, req := range l.queues[peerID] {
			if now.Sub(l.askedAt[req]) < requestDeadline {
				kept = append(kept, req)
				continue
			}
			l.timeouts++
			l.drop(req)
			req.request.Finished()
			req.request.complete(0)
			l.h.fail(peerID, req.height, client.ErrPeerNotResponded)
		}
		l.queues[peerID] = kept
	}
	// the downlink carries the next block of this many peers, in turn
	for sent, tried := 0, 0; sent < l.downlink && tried < len(l.peers); tried++ {
		peerID := l.peers[l.turn%len(l.peers)]
		l.turn++
		if queue := l.queues[peerID]; len(queue) > 0 {
			queue[0].endAt = now
			l.slowest = max(l.slowest, now.Sub(l.askedAt[queue[0]]))
			l.queues[peerID] = queue[1:]
			sent++
		}
	}
	for {
		consumed := l.consume(now)
		produced := l.produce(now)
		for _, req := range l.inFlight {
			if _, queued := l.askedAt[req]; queued {
				continue
			}
			l.askedAt[req] = now
			req.endAt = never
			l.queues[req.peer.peerID] = append(l.queues[req.peer.peerID], req)
		}
		if !consumed && !produced {
			return
		}
	}
}

// drop takes req out of the requests in flight.
func (l *linkRun) drop(req *tipRequest) {
	for i, other := range l.inFlight {
		if other == req {
			l.inFlight = append(l.inFlight[:i], l.inFlight[i+1:]...)
			return
		}
	}
}

// TestBlockSyncFarBehindOnASharedDownlink checks the same for a node whose
// downlink carries four blocks a second in all, as much as the four peers a
// window of requests is spread over can send. Each block must still be fetched
// about once, and no request may run into the request deadline.
func (suite *SynchronizerTestSuite) TestBlockSyncFarBehindOnASharedDownlink() {
	const (
		height = int64(100)
		blocks = int64(600)
		peers  = 12
	)
	suite.client.On("Send", mock.Anything, mock.AnythingOfType("p2p.PeerError")).Maybe().Return(nil)
	h := suite.newWaitForSyncHarness(height)
	defer h.cancel()
	// the node is re-syncing history committed long ago
	h.setLastBlockTime(h.clock.Now().Add(-365 * 24 * time.Hour))
	l := &linkRun{
		tipRun: &tipRun{
			h:          h,
			blockTimes: map[int64]time.Time{},
			suppliedBy: map[types.NodeID]int{},
		},
		downlink: 4,
		queues:   map[types.NodeID][]*tipRequest{},
		askedAt:  map[*tipRequest]time.Time{},
	}
	for i := 0; i < peers; i++ {
		peerID := types.NodeID(fmt.Sprintf("peer %d", i))
		l.peers = append(l.peers, peerID)
		h.sync.AddPeer(newPeerData(peerID, 1, height+blocks-1))
	}

	elapsed, _, ended := h.run(2*time.Hour, l.step)
	suite.T().Logf("%d blocks applied in %s, %d block responses downloaded, %d requests timed out, %d of %d peers left, slowest answer %s",
		l.applied, elapsed, l.blocksSent, l.timeouts, h.sync.peerStore.Len(), peers, l.slowest)
	suite.Require().True(ended, "not caught up after %s: %d blocks applied, %d requests timed out, %d of %d peers left",
		elapsed, l.applied, l.timeouts, h.sync.peerStore.Len(), peers)
	suite.Require().LessOrEqual(elapsed, 5*time.Minute,
		"%d blocks applied in %s, %d block responses downloaded, slowest answer %s; four peers alone send them in under four minutes",
		l.applied, elapsed, l.blocksSent, l.slowest)
	suite.Require().Zero(l.timeouts,
		"%d blocks applied in %s, %d block responses downloaded, %d requests timed out, %d of %d peers left",
		l.applied, elapsed, l.blocksSent, l.timeouts, h.sync.peerStore.Len(), peers)
	suite.Require().LessOrEqual(l.blocksSent, 2*l.applied,
		"%d blocks applied in %s, %d block responses downloaded from %d peers", l.applied, elapsed, l.blocksSent, peers)
}
