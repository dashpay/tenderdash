package blocksync

import (
	"time"
)

// TestWaitForSyncStallBackstopIgnoresAFarClaimOnARecentBlock checks the stall
// backstop against a far claim made while another peer is within maxCatchupGap
// and the node's last block is less than staleBlockAge old. The claim is not
// verified, so it must not keep the stalled node in block sync.
func (suite *SynchronizerTestSuite) TestWaitForSyncStallBackstopIgnoresAFarClaimOnARecentBlock() {
	const height = int64(100)
	near := newPeerData("near peer", 1, height+maxCatchupGap/2)
	h := suite.newWaitForSyncHarness(height, near, newPeerData("claimant", 1, maxPlausiblePeerHeight))
	defer h.cancel()
	h.setLastBlockTime(h.clock.Now().Add(-staleBlockAge / 2))

	elapsed, caughtUp, ended := h.run(maxSyncStall+time.Minute, nil)
	suite.Require().True(ended, "a far claim kept a node stalled on a recent block in block sync")
	suite.Require().False(caughtUp)
	suite.Require().Greater(elapsed, maxSyncStall)
}
