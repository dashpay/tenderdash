package statesync

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/fortytw2/leaktest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/test/factory"
	ssproto "github.com/dashpay/tenderdash/proto/tendermint/statesync"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// adversarialResponder answers light block requests per peer: peers in forging
// answer at once with a forged threshold block signature (the header chain and
// ValidateBasic still pass, only VerifyCommit rejects it), every other peer
// answers after honestDelay. The delay is what lets a forging peer pipeline
// several responses into the queue before the serialized verify loop judges its
// first one.
//
// When answerNil is set, the delayed answer is "I don't have that block", which
// drops the peer from the dispatch pool without any rejection being reported for
// it - the path a stalled run has to notice on its own.
type adversarialResponder struct {
	chain       map[int64]*types.LightBlock
	forging     map[types.NodeID]struct{}
	honestDelay time.Duration
	answerNil   bool
	stopHeight  uint64
}

// run answers requests until ctx is canceled or closeCh is closed.
func (a adversarialResponder) run(
	ctx context.Context,
	t *testing.T,
	receiving, sending chan p2p.Envelope,
	closeCh chan struct{},
) {
	t.Helper()
	var wg sync.WaitGroup
	defer wg.Wait()

	answer := func(to types.NodeID, lb *tmproto.LightBlock, delay time.Duration) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return
				}
			}
			sendMsgToChan(ctx, sending, newLBMessage(to, lb))
		}()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-closeCh:
			return
		case envelope := <-receiving:
			msg, ok := envelope.Message.(*ssproto.LightBlockRequest)
			if !ok || msg.Height < a.stopHeight {
				continue
			}
			lb, err := a.chain[int64(msg.Height)].ToProto()
			require.NoError(t, err)

			if _, forges := a.forging[envelope.To]; !forges {
				if a.answerNil {
					lb = nil
				}
				answer(envelope.To, lb, a.honestDelay)
				continue
			}
			sig := lb.SignedHeader.Commit.ThresholdBlockSignature
			forged := make([]byte, len(sig))
			copy(forged, sig)
			forged[0] ^= 0xFF
			lb.SignedHeader.Commit.ThresholdBlockSignature = forged
			answer(envelope.To, lb, 0)
		}
	}
}

// adversarialRun backfills startHeight..stopHeight from peers of which those in
// forging answer with commits that cannot be verified.
type adversarialRun struct {
	peerIDs     []string
	forging     map[types.NodeID]struct{}
	honestDelay time.Duration
	answerNil   bool
	// responseTimeout must exceed honestDelay, otherwise honest answers arrive
	// after the fetcher has given up and no peer ever serves a block.
	responseTimeout         time.Duration
	startHeight, stopHeight int64
}

// exec runs the backfill and reports how long it took, so a test can tell a
// fail-fast apart from a run that merely died with the context.
func (run adversarialRun) exec(ctx context.Context, t *testing.T) (*reactorTestSuite, time.Duration, error) {
	t.Helper()

	startHeight, stopHeight := run.startHeight, run.stopHeight
	stopTime := time.Date(2020, 1, 1, 0, 100, 0, 0, time.UTC)
	rts := setup(ctx, t, nil, nil, nil, uint(startHeight-stopHeight)+5)

	for _, peer := range run.peerIDs {
		rts.peerUpdateCh <- p2p.PeerUpdate{
			NodeID: types.NodeID(peer),
			Status: p2p.PeerStatusUp,
			Channels: p2p.ChannelIDSet{
				SnapshotChannel:   struct{}{},
				ChunkChannel:      struct{}{},
				LightBlockChannel: struct{}{},
				ParamsChannel:     struct{}{},
			},
		}
	}
	rts.stateStore.
		On("SaveValidatorSets",
			mock.AnythingOfType("int64"),
			mock.AnythingOfType("int64"),
			mock.AnythingOfType("*types.ValidatorSet")).
		Maybe().
		Return(nil)

	chain := buildLightBlockChain(ctx, t, stopHeight-1, startHeight+1, stopTime, rts.privVal)

	closeCh := make(chan struct{})
	defer close(closeCh)
	responder := adversarialResponder{
		chain:       chain,
		forging:     run.forging,
		honestDelay: run.honestDelay,
		answerNil:   run.answerNil,
		stopHeight:  uint64(stopHeight),
	}
	go responder.run(ctx, t, rts.blockOutCh, rts.blockInCh, closeCh)

	started := time.Now()
	err := rts.reactor.backfill(
		ctx,
		factory.DefaultTestChainID,
		startHeight,
		stopHeight,
		1,
		factory.MakeBlockIDWithHash(chain[startHeight].Hash()),
		stopTime,
		10*time.Millisecond,
		run.responseTimeout,
	)
	return rts, time.Since(started), err
}

// TestReactor_Backfill_SurvivesFastMaliciousPeer reproduces the exploit the peer
// quarantine is meant to close: a malicious peer that answers faster than the
// verify loop can judge its commits pipelines many forged responses into the
// queue before the first one is rejected. Each rejection must cost the malicious
// peer its place in the dispatch pool, not a slice of the retry budget shared
// with the honest peer, otherwise the budget is exhausted and the run hard-fails
// while a perfectly good peer is still serving.
func TestReactor_Backfill_SurvivesFastMaliciousPeer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	const (
		startHeight int64 = 60
		stopHeight  int64 = 11
	)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	t.Cleanup(leaktest.CheckTimeout(t, 1*time.Minute))

	// The pool hands out peers in order, so the honest peer takes the first
	// height. That is the whole race: the verify loop is held at that height for
	// as long as the honest answer takes, while the malicious peer races ahead
	// filling the queue with forged commits for every height behind it. A
	// malicious peer that wins the first height instead is caught on the very
	// first verification and never gets to pipeline anything.
	peerIDs := genPeerIDs(2)
	rts, _, err := adversarialRun{
		peerIDs:         peerIDs,
		forging:         map[types.NodeID]struct{}{types.NodeID(peerIDs[1]): {}},
		honestDelay:     300 * time.Millisecond,
		responseTimeout: 5 * time.Second,
		startHeight:     startHeight,
		stopHeight:      stopHeight,
	}.exec(ctx, t)

	require.NoError(t, err, "a peer outracing the verify loop must not exhaust the retry budget shared with honest peers")
	for height := stopHeight; height <= startHeight; height++ {
		require.NotNil(t, rts.blockStore.LoadBlockMeta(height),
			"every height must be backfilled from the honest peer, height %d", height)
	}
}

// TestReactor_Backfill_SurvivesSybilSwarm reproduces the same exhaustion without
// any timing advantage: enough distinct malicious peers, each rejected once,
// drain a retry budget that is global rather than per-peer. Eviction, not the
// budget, has to be what bounds a misbehaving peer.
func TestReactor_Backfill_SurvivesSybilSwarm(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	const (
		startHeight  int64 = 60
		stopHeight   int64 = 11
		maliciousQty       = 12
		honestQty          = 4
		totalPeerQty       = maliciousQty + honestQty
	)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	t.Cleanup(leaktest.CheckTimeout(t, 1*time.Minute))

	peerIDs := genPeerIDs(totalPeerQty)
	forging := make(map[types.NodeID]struct{}, maliciousQty)
	for _, peer := range peerIDs[:maliciousQty] {
		forging[types.NodeID(peer)] = struct{}{}
	}

	rts, _, err := adversarialRun{
		peerIDs:         peerIDs,
		forging:         forging,
		responseTimeout: 5 * time.Second,
		startHeight:     startHeight,
		stopHeight:      stopHeight,
	}.exec(ctx, t)

	require.NoError(t, err, "a Sybil swarm must be bounded by eviction, not by the shared retry budget")
	for height := stopHeight; height <= startHeight; height++ {
		require.NotNil(t, rts.blockStore.LoadBlockMeta(height),
			"every height must be backfilled from the honest peers, height %d", height)
	}
}

// TestReactor_Backfill_FailsFastWhenNoPeerCanServe asserts the other half of the
// same machinery: once every peer able to serve is gone, the run must end with
// the rejection that emptied the pool rather than block until the context
// expires - and it must not then report the aborted run as a completed one.
//
// The second peer here leaves the pool by answering "I don't have that block",
// a path that reports no rejection of its own and so cannot notice the stall by
// itself. Its answer is delayed past the first peer's quarantine on purpose: the
// run is only stalled once that last fetch is released, which is the moment the
// check has to run.
func TestReactor_Backfill_FailsFastWhenNoPeerCanServe(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	const (
		startHeight int64 = 60
		stopHeight  int64 = 11
		ctxTimeout        = 45 * time.Second
	)

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	t.Cleanup(leaktest.CheckTimeout(t, 1*time.Minute))

	peerIDs := genPeerIDs(2)
	rts, elapsed, err := adversarialRun{
		peerIDs:         peerIDs,
		forging:         map[types.NodeID]struct{}{types.NodeID(peerIDs[0]): {}},
		honestDelay:     time.Second,
		answerNil:       true,
		responseTimeout: 5 * time.Second,
		startHeight:     startHeight,
		stopHeight:      stopHeight,
	}.exec(ctx, t)

	require.Error(t, err, "a run with no peer left to serve must fail, not report success")
	require.ErrorContains(t, err, "invalid commit",
		"the failure must name the rejection that emptied the pool, not a context deadline")
	require.Less(t, elapsed, ctxTimeout/2,
		"the run must fail fast rather than block until the context expires")
	require.Nil(t, rts.blockStore.LoadBlockMeta(startHeight),
		"no height may be persisted from a peer whose commit failed verification")
}

// TestReactor_Backfill_RejectsUnboundedVoteExtensions covers the cost of commit
// verification: VerifyCommit spends a BLS pairing per threshold
// vote extension and nothing else bounds that peer-controlled list. Duplicates
// are the sharper half - the list's multiplicity is unauthenticated, so a commit
// whose genuine extension is repeated verifies successfully today and the sender
// is never punished for the work it bought.
func TestReactor_Backfill_RejectsUnboundedVoteExtensions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	for _, tc := range []struct {
		name    string
		tamper  func(*types.Commit)
		errText string
	}{
		{
			name: "repeated genuine extension",
			tamper: func(c *types.Commit) {
				genuine := c.ThresholdVoteExtensions[0]
				for i := 0; i < 8; i++ {
					c.ThresholdVoteExtensions = append(c.ThresholdVoteExtensions, genuine)
				}
			},
			errText: "duplicate threshold vote extension",
		},
		{
			name: "more extensions than the bound admits",
			tamper: func(c *types.Commit) {
				for i := 0; i <= maxThresholdVoteExtensions; i++ {
					c.ThresholdVoteExtensions = append(c.ThresholdVoteExtensions, &tmproto.VoteExtension{
						Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
						Extension: []byte(fmt.Sprintf("padding %d", i)),
						Signature: make([]byte, types.SignatureSize),
					})
				}
			},
			errText: "too many threshold vote extensions",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			blockStore, tamperedHeight, err := backfillTamperedCommit(ctx, t, tc.tamper)

			require.Error(t, err, "an unbounded vote-extension list must be rejected before it is verified")
			require.ErrorContains(t, err, tc.errText)
			require.Nil(t, blockStore.LoadBlockMeta(tamperedHeight),
				"a commit rejected at the vote-extension bound must never reach the block store")
		})
	}
}

// TestReactor_Backfill_ReportsUndeliverableBadPeerReport asserts that when the
// reactor cannot even report the peer that supplied an unverifiable commit, the
// aborted run says so. Returning nil there reports a backfill that stopped
// mid-range as completed.
func TestReactor_Backfill_ReportsUndeliverableBadPeerReport(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	undeliverable := errors.New("peer error channel is closed")
	blockStore, tamperedHeight, err := backfillTamperedBlock(ctx, t,
		func(lb *types.LightBlock) {
			forged := make([]byte, len(lb.Commit.ThresholdBlockSignature))
			copy(forged, lb.Commit.ThresholdBlockSignature)
			forged[0] ^= 0xFF
			lb.Commit.ThresholdBlockSignature = forged
		},
		func(rts *reactorTestSuite) {
			rts.reactor.sendBlockError = func(context.Context, p2p.PeerError) error { return undeliverable }
		})

	require.Error(t, err, "a run aborted because a bad peer could not be reported must not look like a completed one")
	require.ErrorIs(t, err, undeliverable)
	require.Nil(t, blockStore.LoadBlockMeta(tamperedHeight),
		"a commit that fails verification must never reach the block store")
}
