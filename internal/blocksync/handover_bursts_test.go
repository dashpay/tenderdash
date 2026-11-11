package blocksync

import (
	"fmt"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/dashpay/tenderdash/types"
)

// TestWaitForSyncHandsOverAtTheTipOfAChainCommittingInBursts checks the handover
// of a node level with a chain that commits blocks only while there are
// transactions: a run of blocks three seconds apart, then a pause until the
// next run. Three peers hold every block and answer at once, so the node
// applies each block within a status round of its commit, and a far range is
// reported by a peer nobody asks. A pause in block production is not the node
// falling behind, so it must hand over as it does on a chain committing a block
// every three seconds without pauses.
func (suite *SynchronizerTestSuite) TestWaitForSyncHandsOverAtTheTipOfAChainCommittingInBursts() {
	const (
		height   = int64(100)
		interval = 3 * time.Second
		limit    = time.Hour
	)
	for _, tc := range []struct {
		name string
		// run is the number of blocks committed interval apart before each pause
		run   int64
		pause time.Duration
	}{
		{name: "no pauses"},
		{name: "12 blocks, then 20 seconds without one", run: 12, pause: 20 * time.Second},
		{name: "12 blocks, then 50 seconds without one", run: 12, pause: 50 * time.Second},
		{name: "12 blocks, then 80 seconds without one", run: 12, pause: 80 * time.Second},
	} {
		suite.Run(tc.name, func() {
			suite.client.On("Send", mock.Anything, mock.AnythingOfType("p2p.PeerError")).Maybe().Return(nil)
			h := suite.newWaitForSyncHarness(height)
			defer h.cancel()
			start := h.clock.Now()
			r := &tipRun{
				h:          h,
				interval:   interval,
				blockTimes: map[int64]time.Time{},
				suppliedBy: map[types.NodeID]int{},
				tip:        height - 1,
				nextCommit: start,
			}
			for i := 0; i < 3; i++ {
				r.reporting = append(r.reporting, types.NodeID(fmt.Sprintf("holder %d", i)))
			}
			// the block store holds the blocks the node applied before it stopped
			var stored []time.Time
			for i := maxCatchupGap; i >= 0; i-- {
				stored = append(stored, start.Add(-time.Duration(i+1)*interval))
			}
			h.sync.mtx.Lock()
			h.sync.blockTimes = stored
			h.sync.mtx.Unlock()
			h.sync.AddPeer(newPeerData("far range", 1_000_000_000, maxPlausiblePeerHeight))

			breaks := 0
			elapsed, caughtUp, ended := h.run(limit, func(elapsed time.Duration) {
				// the gap after the block about to be committed
				r.interval = interval
				if next := r.tip + 1 - height; tc.run > 0 && next%tc.run == tc.run-1 {
					r.interval = tc.pause
				}
				r.step(elapsed)
				st := h.sync.stallSnapshot()
				if !atTip(st.lastBlockTime, st.tipWindow, st.precision, h.clock.Now().Add(time.Second)) {
					breaks++
				}
			})
			suite.Require().True(ended,
				"still in block sync after %s: %d blocks applied, each within a status round of its commit; the node counted as away from the tip at %d ticks",
				elapsed, r.applied, breaks)
			suite.Require().False(caughtUp)
			suite.T().Logf("handed over after %s: %d blocks applied, away from the tip at %d ticks", elapsed, r.applied, breaks)
		})
	}
}
