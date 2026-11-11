package blocksync

import (
	"errors"
	"fmt"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/dashpay/tenderdash/internal/p2p/client"
	"github.com/dashpay/tenderdash/types"
)

// requestDeadline is how long the block client waits for a peer's response.
const requestDeadline = 60 * time.Second

// tipRun drives the producer and the consumer of a node following a chain that
// commits a block every interval. Jobs are handed out as produceJob and nextJob
// do: shouldJobBeGenerated admits them, nextHeight picks the height and
// reservePeer the peer; a height no peer advertises yet is held until one does,
// as getPeer holds it. Once a second, as askEveryPeer does, the peers
// reserveNextBlockPeers returns are asked for the height to apply. Responses are
// queued and applied in height order as consumeJobResult and applyBlock do. A
// peer listed in declining answers at once that it has no such block; any other
// answers with the block, a peer listed in hold that long after it is asked and
// the rest at once. The peers in reporting report the chain's height every
// status round.
type tipRun struct {
	h         *waitForSyncHarness
	interval  time.Duration
	hold      map[types.NodeID]time.Duration
	declining map[types.NodeID]bool
	// reporting are the peers that report the chain's height, in the order
	// their reports arrive.
	reporting []types.NodeID

	blockTimes map[int64]time.Time
	tip        int64
	nextCommit time.Time
	inFlight   []*tipRequest
	// waiting is the height the producer holds until a peer advertises it.
	waiting int64

	applied    int
	suppliedBy map[types.NodeID]int
	// blocksSent counts the requests answered with a block.
	blocksSent int
	// oldest is the greatest age the last applied block reached.
	oldest time.Duration
}

type tipRequest struct {
	peer    PeerData
	request *serviceRequest
	height  int64
	endAt   time.Time
}

func (r *tipRun) step(elapsed time.Duration) {
	now := r.h.clock.Now()
	for !now.Before(r.nextCommit) {
		r.tip++
		r.blockTimes[r.tip] = r.nextCommit
		r.nextCommit = r.nextCommit.Add(r.interval)
	}
	if elapsed%(statusUpdateIntervalSeconds*time.Second) == 0 {
		for _, id := range r.reporting {
			r.h.sync.AddPeer(newPeerData(id, 1, r.tip))
		}
	}
	for r.consume(now) || r.produce(now) {
	}
	height, peers := r.h.sync.reserveNextBlockPeers()
	for _, peer := range peers {
		r.request(peer, height, now)
	}
	for r.consume(now) || r.produce(now) {
	}
	st := r.h.sync.stallSnapshot()
	// the age the next tick reads
	r.oldest = max(r.oldest, now.Add(time.Second).Sub(st.lastBlockTime))
}

// consume hands the consumer every response due by now and applies what can be
// applied, and reports whether there was any.
func (r *tipRun) consume(now time.Time) bool {
	sync := r.h.sync
	var waiting []*tipRequest
	consumed := false
	for _, req := range r.inFlight {
		if now.Before(req.endAt) {
			waiting = append(waiting, req)
			continue
		}
		consumed = true
		r.blocksSent++
		req.request.Finished()
		req.request.complete(1000)
		sync.peerStore.Update(req.peer.peerID, AddNumPending(-1), ResetFailures())
		err := sync.addBlock(BlockResponse{
			PeerID: req.peer.peerID,
			Block:  &types.Block{Header: types.Header{Height: req.height}},
		})
		if err != nil && !errors.Is(err, errDuplicateBlock) {
			r.h.suite.Require().NoError(err)
		}
		for {
			resp, ok := sync.getPendingResponse()
			if !ok {
				break
			}
			sync.advance(r.blockTimes[resp.Block.Height], defaultPrecision)
			r.applied++
			r.suppliedBy[resp.PeerID]++
		}
	}
	r.inFlight = waiting
	return consumed
}

// produce hands out every job due now and reports whether there was any.
func (r *tipRun) produce(now time.Time) bool {
	gen := r.h.sync.jobGen
	produced := false
	for {
		height := r.waiting
		if height == 0 {
			if !gen.shouldJobBeGenerated(r.h.sync.backlog()) {
				return produced
			}
			height = gen.nextHeight()
		}
		peer, found := r.h.sync.peerStore.reservePeer(height)
		if !found {
			r.waiting = height
			return produced
		}
		r.waiting = 0
		r.request(peer, height, now)
		produced = true
	}
}

// request has peer answer a request for height as its kind does.
func (r *tipRun) request(peer PeerData, height int64, now time.Time) {
	request := peer.service.request()
	request.Started()
	if r.declining[peer.peerID] {
		request.Finished()
		request.complete(0)
		r.h.fail(peer.peerID, height, &client.ErrBlockNotFound{Height: height})
		return
	}
	r.inFlight = append(r.inFlight, &tipRequest{
		peer:    peer,
		request: request,
		height:  height,
		endAt:   now.Add(r.hold[peer.peerID]),
	})
}

// TestWaitForSyncHandsOverBehindAPeerAnsweringJustInsideTheDeadline checks the
// handover of a node that restarts three blocks behind the network, with a
// prompt peer connected, while a far range is reported by a peer nobody asks.
// One more peer answers every request with the block, one second inside the
// request deadline, and never fails one.
//
// On a chain committing a block every three minutes the node hands over within
// minutes whichever peer reports first. On one committing a block every few
// seconds it must as well.
func (suite *SynchronizerTestSuite) TestWaitForSyncHandsOverBehindAPeerAnsweringJustInsideTheDeadline() {
	const (
		height = int64(100)
		prompt = types.NodeID("prompt peer")
		slow   = types.NodeID("peer answering inside the deadline")
		behind = 3
		limit  = time.Hour
	)
	for _, tc := range []struct {
		name      string
		interval  time.Duration
		reporting []types.NodeID
	}{
		{name: "a block every 3m, the prompt peer reports first", interval: 3 * time.Minute, reporting: []types.NodeID{prompt, slow}},
		{name: "a block every 3m, the other peer reports first", interval: 3 * time.Minute, reporting: []types.NodeID{slow, prompt}},
		{name: "a block every 5s, the prompt peer reports first", interval: 5 * time.Second, reporting: []types.NodeID{prompt, slow}},
		{name: "a block every 5s, the other peer reports first", interval: 5 * time.Second, reporting: []types.NodeID{slow, prompt}},
		{name: "a block every 2s, the other peer reports first", interval: 2 * time.Second, reporting: []types.NodeID{slow, prompt}},
		{name: "a block every second, the other peer reports first", interval: time.Second, reporting: []types.NodeID{slow, prompt}},
	} {
		suite.Run(tc.name, func() {
			suite.client.On("Send", mock.Anything, mock.AnythingOfType("p2p.PeerError")).Maybe().Return(nil)
			h := suite.newWaitForSyncHarness(height)
			defer h.cancel()
			start := h.clock.Now()
			r := &tipRun{
				h:          h,
				interval:   tc.interval,
				hold:       map[types.NodeID]time.Duration{slow: requestDeadline - time.Second},
				reporting:  tc.reporting,
				blockTimes: map[int64]time.Time{},
				suppliedBy: map[types.NodeID]int{},
				tip:        height - 1 + behind,
			}
			// the last committed block is two seconds old
			for i := int64(0); i < behind; i++ {
				r.blockTimes[r.tip-i] = start.Add(-2*time.Second - time.Duration(i)*tc.interval)
			}
			r.nextCommit = r.blockTimes[r.tip].Add(tc.interval)
			h.sync.AddPeer(newPeerData("far range", 1_000_000_000, maxPlausiblePeerHeight))

			elapsed, caughtUp, ended := h.run(limit, r.step)
			suite.Require().True(ended,
				"still in block sync after %s: %d blocks applied (%s), the last applied block never older than %s",
				elapsed, r.applied, fmt.Sprint(r.suppliedBy), r.oldest)
			suite.Require().False(caughtUp)
			suite.T().Logf("handed over after %s: %d blocks applied (%s), the last applied block never older than %s",
				elapsed, r.applied, fmt.Sprint(r.suppliedBy), r.oldest)
		})
	}
}

// TestWaitForSyncFetchesEachBlockAtTheTipFromOnePeer checks how many times a
// node following the network fetches each new block while it is still in block
// sync. Five peers hold every block and answer at once. One more peer reports a
// far height and answers every request that it has no such block; it is the
// only peer advertising the next height until the others report it, so it is
// asked for it first. Its answer must not have every block fetched from all
// five peers: the request the producer sends once a peer reports the block
// brings it before the others are asked.
func (suite *SynchronizerTestSuite) TestWaitForSyncFetchesEachBlockAtTheTipFromOnePeer() {
	const (
		height   = int64(100)
		interval = 5 * time.Second
		claimant = types.NodeID("claimant")
		holders  = 5
	)
	suite.client.On("Send", mock.Anything, mock.AnythingOfType("p2p.PeerError")).Maybe().Return(nil)
	h := suite.newWaitForSyncHarness(height)
	defer h.cancel()
	start := h.clock.Now()
	r := &tipRun{
		h:          h,
		interval:   interval,
		declining:  map[types.NodeID]bool{claimant: true},
		blockTimes: map[int64]time.Time{height: start.Add(-2 * time.Second)},
		suppliedBy: map[types.NodeID]int{},
		tip:        height,
		nextCommit: start.Add(interval - 2*time.Second),
	}
	for i := 0; i < holders; i++ {
		r.reporting = append(r.reporting, types.NodeID(fmt.Sprintf("holder %d", i)))
	}
	h.sync.AddPeer(newPeerData(claimant, 1, maxPlausiblePeerHeight))

	elapsed, _, ended := h.run(tipGrace+time.Minute, r.step)
	suite.Require().True(ended)
	suite.T().Logf("%d blocks applied in %s, %d block responses fetched (%s)", r.applied, elapsed, r.blocksSent, fmt.Sprint(r.suppliedBy))
	suite.Require().Greater(r.applied, 10)
	suite.Require().LessOrEqual(r.blocksSent, 2*r.applied,
		"%d blocks applied in %s, %d block responses fetched from %d peers",
		r.applied, elapsed, r.blocksSent, holders)
}

// TestWaitForSyncHandsOverPastAFarClaimFromNewPeersOnAChainHaltedForLong checks
// that a far claim made by a new peer every minute does not keep a node in block
// sync on a chain that stopped producing blocks, up to staleBlockAge ago.
// Validators restarted to bring such a chain back reach consensus only through
// this handover.
func (suite *SynchronizerTestSuite) TestWaitForSyncHandsOverPastAFarClaimFromNewPeersOnAChainHaltedForLong() {
	const (
		height  = int64(100)
		tipPeer = types.NodeID("tip peer")
	)
	for _, tc := range []struct {
		name string
		// haltedFor is the age of the last block when the node starts
		haltedFor time.Duration
	}{
		{name: "half an hour", haltedFor: 30 * time.Minute},
		{name: "55 minutes", haltedFor: 55 * time.Minute},
		{name: "two hours", haltedFor: 2 * time.Hour},
		{name: "just under the stale age", haltedFor: staleBlockAge - time.Minute},
	} {
		suite.Run(tc.name, func() {
			suite.client.On("Send", mock.Anything, mock.AnythingOfType("p2p.PeerError")).Maybe().Return(nil)
			h := suite.newWaitForSyncHarness(height, newPeerData(tipPeer, 1, height-1))
			defer h.cancel()
			h.setLastBlockTime(h.clock.Now().Add(-tc.haltedFor))

			elapsed, caughtUp, ended := h.run(maxSyncStall+time.Minute, h.newPeerEveryMinute())
			suite.Require().True(ended,
				"still in block sync after %s on a chain halted for %s, behind a claim moved to a new peer every minute",
				elapsed, tc.haltedFor)
			suite.Require().False(caughtUp)
		})
	}
}
