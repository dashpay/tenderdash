package blocksync

import (
	"fmt"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/dashpay/tenderdash/internal/p2p/client"
	"github.com/dashpay/tenderdash/types"
)

// lateSupplyRun drives a node that restarted a few blocks behind a chain
// committing a block every three minutes. Requests for the height the node waits
// for are handed out as the job generator does: the first by the peer store,
// further ones by the generator once one fails or goes unanswered. The peers
// named late answer a request only once the block is lateBy old, or answer just
// inside the request deadline that they have no such block when that is further
// off; the peer named prompt answers at once. Every peer reports each block as soon as it is committed, and one more
// peer reports a far range it is never asked for.
type lateSupplyRun struct {
	h        *waitForSyncHarness
	prompt   types.NodeID
	lateBy   time.Duration
	interval time.Duration
	// blockTimes maps a committed height to its block time.
	blockTimes map[int64]time.Time
	tip        int64
	nextCommit time.Time
	// outstanding are the requests for the height the node waits for.
	outstanding []*lateRequest
	// failed records the heights a request failed for.
	failed map[int64]bool
	// servedByPrompt counts the blocks the prompt peer was asked for.
	servedByPrompt int
	served         int
}

type lateRequest struct {
	peer    PeerData
	request *serviceRequest
	height  int64
	endAt   time.Time
	deliver bool
}

func (r *lateSupplyRun) ids() []types.NodeID {
	var ids []types.NodeID
	for _, peer := range r.h.sync.peerStore.All() {
		if peer.base == 1 {
			ids = append(ids, peer.peerID)
		}
	}
	return ids
}

func (r *lateSupplyRun) commit(blockTime time.Time) {
	r.tip++
	r.blockTimes[r.tip] = blockTime
	for _, id := range r.ids() {
		r.h.sync.AddPeer(newPeerData(id, 1, r.tip))
	}
}

func (r *lateSupplyRun) apply(peer PeerData, request *serviceRequest, height int64) {
	sync := r.h.sync
	request.Finished()
	request.complete(1000)
	sync.peerStore.Update(peer.peerID, AddNumPending(-1), ResetFailures())
	if applied, _ := sync.GetStatus(); height < applied {
		return
	}
	r.h.suite.Require().NoError(sync.addBlock(BlockResponse{
		PeerID: peer.peerID,
		Block:  &types.Block{Header: types.Header{Height: height}},
	}))
	sync.advance(peer.peerID, r.blockTimes[height], defaultPrecision)
	r.served++
}

func (r *lateSupplyRun) step(time.Duration) {
	sync := r.h.sync
	now := r.h.clock.Now()
	for !now.Before(r.nextCommit) {
		r.commit(r.nextCommit)
		r.nextCommit = r.nextCommit.Add(r.interval)
	}
	var waiting []*lateRequest
	for _, out := range r.outstanding {
		switch {
		case now.Before(out.endAt):
			waiting = append(waiting, out)
		case out.deliver:
			r.apply(out.peer, out.request, out.height)
		default:
			out.request.Finished()
			out.request.complete(0)
			r.failed[out.height] = true
			r.h.fail(out.peer.peerID, out.height, &client.ErrBlockNotFound{Height: out.height})
		}
	}
	r.outstanding = waiting
	for {
		height, _ := sync.GetStatus()
		if height > r.tip {
			return
		}
		sync.jobGen.shouldJobBeGenerated(height, 0)
		peer, spreadHeight, found := sync.jobGen.nextSpread()
		if found && spreadHeight != height {
			return
		}
		if !found {
			if len(r.outstanding) > 0 {
				return
			}
			if peer, found = sync.peerStore.reservePeer(height); !found {
				return
			}
			if r.failed[height] {
				sync.jobGen.markRetried(height, peer.peerID)
			}
		}
		r.ask(peer, height, now)
	}
}

// ask has peer answer a request for height as its kind does.
func (r *lateSupplyRun) ask(peer PeerData, height int64, now time.Time) {
	request := peer.service.request()
	request.Started()
	if peer.peerID == r.prompt {
		r.servedByPrompt++
		r.apply(peer, request, height)
		return
	}
	deliverAt := r.blockTimes[height].Add(r.lateBy)
	if !deliverAt.After(now) {
		r.apply(peer, request, height)
		return
	}
	out := &lateRequest{peer: peer, request: request, height: height}
	if deliverAt.Sub(now) < syncTimeout {
		out.endAt, out.deliver = deliverAt, true
	} else {
		out.endAt = now.Add(syncTimeout - time.Second)
	}
	r.outstanding = append(r.outstanding, out)
}

// TestWaitForSyncHandsOverBehindPeersSupplyingEachBlockLate checks the handover
// of a node that restarts three blocks behind a chain committing a block every
// three minutes, with a prompt peer connected, while a far range is reported by
// a peer nobody asks. The node receives every block within its at-tip window.
//
// When the prompt peer reports first it supplies every block and the node hands
// over two minutes after reaching the tip. When a few peers that supply each
// block about three minutes late report first, they are asked for every block:
// before any block is applied no peer has backed its claim, a due recovery probe
// goes before a peer with no record, and each block then credits the late peer
// that supplied it. The prompt peer is never asked, and the node must still
// hand over.
func (suite *SynchronizerTestSuite) TestWaitForSyncHandsOverBehindPeersSupplyingEachBlockLate() {
	const (
		height   = int64(100)
		prompt   = types.NodeID("prompt peer")
		interval = 3 * time.Minute
		// late enough that less than tipGrace of the at-tip window is left
		lateBy = tipRecency - tipGrace + 5*time.Second
		limit  = time.Hour
	)
	for _, tc := range []struct {
		name        string
		promptFirst bool
	}{
		{name: "prompt peer reports first", promptFirst: true},
		{name: "late peers report first"},
	} {
		suite.Run(tc.name, func() {
			suite.client.On("Send", mock.Anything, mock.AnythingOfType("p2p.PeerError")).Maybe().Return(nil)
			h := suite.newWaitForSyncHarness(height)
			defer h.cancel()
			start := h.clock.Now()
			// the node was down for ten minutes
			h.setLastBlockTime(start.Add(-10 * time.Minute))

			r := &lateSupplyRun{
				h:          h,
				prompt:     prompt,
				lateBy:     lateBy,
				interval:   interval,
				blockTimes: map[int64]time.Time{},
				failed:     map[int64]bool{},
				tip:        height - 1,
			}
			if tc.promptFirst {
				h.sync.AddPeer(newPeerData(prompt, 1, r.tip))
			}
			for i := 0; i < 6; i++ {
				h.sync.AddPeer(newPeerData(types.NodeID(fmt.Sprintf("late peer %d", i)), 1, r.tip))
			}
			if !tc.promptFirst {
				h.sync.AddPeer(newPeerData(prompt, 1, r.tip))
			}
			h.sync.AddPeer(newPeerData("far range", 1_000_000_000, maxPlausiblePeerHeight))
			// the three blocks committed while the node was down
			for _, age := range []time.Duration{7 * time.Minute, 4 * time.Minute, time.Minute} {
				r.commit(start.Add(-age))
			}
			r.nextCommit = start.Add(2 * time.Minute)

			elapsed, caughtUp, ended := h.run(limit, r.step)
			suite.Require().True(ended,
				"still in block sync after %s: %d blocks applied, each within the at-tip window, %d of them from the prompt peer",
				elapsed, r.served, r.servedByPrompt)
			suite.Require().False(caughtUp)
		})
	}
}

// TestWaitForSyncKeepsSyncingFarBehindPeersThatFailEveryRequest checks that a
// node far behind the network, by the time of its last block and by every
// claim, is not handed over to consensus because its peers, which earlier
// supplied blocks, fail every request for longer than the stall backstop.
// Consensus catch-up does not close such a gap, and block sync resumes as soon
// as a peer serves again (dashpay/tenderdash#1413).
func (suite *SynchronizerTestSuite) TestWaitForSyncKeepsSyncingFarBehindPeersThatFailEveryRequest() {
	const height = int64(24175)
	suite.client.On("Send", mock.Anything, mock.AnythingOfType("p2p.PeerError")).Maybe().Return(nil)
	var peers []PeerData
	for i := 0; i < 5; i++ {
		peer := newPeerData(types.NodeID(fmt.Sprintf("peer %d", i)), 1, 300_000)
		// each supplied blocks the node applied before the stall
		peer.supplied = true
		peers = append(peers, peer)
	}
	h := suite.newWaitForSyncHarness(height, peers...)
	defer h.cancel()
	// the node is re-syncing history committed long ago
	h.setLastBlockTime(h.clock.Now().Add(-365 * 24 * time.Hour))

	elapsed, _, ended := h.run(maxSyncStall+2*time.Minute, func(elapsed time.Duration) {
		if elapsed == 0 || elapsed%syncTimeout != 0 {
			return
		}
		// every peer times out the request it holds and reports its range again
		for _, peer := range peers {
			h.fail(peer.peerID, height, client.ErrPeerNotResponded)
			h.sync.AddPeer(newPeerData(peer.peerID, 1, 300_000))
		}
	})
	suite.Require().False(ended,
		"handed over after %s, 275825 blocks behind, although five peers still report the next height", elapsed)
}
