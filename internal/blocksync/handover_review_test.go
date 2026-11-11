package blocksync

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

// TestWaitForSyncStallBackstopIgnoresAFarClaimNoPeerBacked checks the stall
// backstop against a far claim from a peer that has supplied nothing, made while
// another peer that has backed its claim is within maxCatchupGap. Only a backed
// claim may keep a stalled node in block sync, so the distance that decides it
// must come from backed claims too.
func (suite *SynchronizerTestSuite) TestWaitForSyncStallBackstopIgnoresAFarClaimNoPeerBacked() {
	const height = int64(100)
	near := newPeerData("near peer", 1, height+maxCatchupGap/2)
	near.supplied = true
	h := suite.newWaitForSyncHarness(height, near, newPeerData("claimant", 1, maxPlausiblePeerHeight))
	defer h.cancel()
	h.setLastBlockTime(h.clock.Now().Add(-time.Hour))

	elapsed, caughtUp, ended := h.run(maxSyncStall+time.Minute, nil)
	suite.Require().True(ended, "a far claim nobody backed kept a stalled node in block sync")
	suite.Require().False(caughtUp)
	suite.Require().Greater(elapsed, maxSyncStall)
}

// TestNextJobDoesNotWaitBehindAHeightNoIdlePeerServes checks that a height
// pushed back for a retry, which an idle peer advertises, is requested while the
// producer waits for a peer to serve a higher height only a busy peer claims.
func TestNextJobDoesNotWaitBehindAHeightNoIdlePeerServes(t *testing.T) {
	const retry = int64(100)
	busy := newPeerData("busy claimant", 1, 1000)
	busy.numPending = maxPendingRequestsPerPeer
	store := NewInMemPeerStore(newPeerData("idle peer", 1, retry+5), busy)
	gen := newJobGenerator(retry+10, log.NewNopLogger(), nil, store)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := gen.nextJob(ctx)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	gen.pushBack(retry)

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-ctx.Done():
		require.Fail(t, "producer still waiting for a peer for a height only a busy peer claims",
			"height %d stays queued although %s can serve it", retry, types.NodeID("idle peer"))
	}
}
