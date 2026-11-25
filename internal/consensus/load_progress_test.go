package consensus

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	tmpubsub "github.com/dashpay/tenderdash/internal/pubsub"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// heightProgressDeadline is how long the node is given to finish the height it
// is on while an attacker holds every connection slot but one.
//
// It is registered before the run rather than read off it. The figure is the
// scheduling delay the design point implies — an honest precommit waits about
// two seconds behind a full rotation, and a height needs a proposal, a prevote
// and a precommit through the same rotation — with a generous multiple on top,
// because unlike everything else in this suite it is measured against the wall
// clock and therefore against whatever machine it runs on. A node that misses
// it is not slow, it is stalled.
const heightProgressDeadline = 60 * time.Second

// Every other measurement in this suite is taken against a fake clock, which is
// what makes them exact. Exactness is not the same as being true of a real
// node: a fake clock cannot show that the signature checks, the application
// round-trip, the write-ahead log and the round timeouts all still fit together
// while a flood is running.
//
// This is the test that shows it. A real node, real BLS, a real application, a
// real write-ahead log, the wall clock — and an attacker holding every
// connection slot but one, sending as fast as the node will take it for as long
// as the height lasts. The node has to finish the height anyway, which it can
// only do by verifying the one honest peer's votes.
func TestLoadHeightProgressUnderSustainedFlood(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Two validators, so this node cannot finish the height on its own: it
	// needs the other one's prevote and precommit, and those arrive over a peer
	// lane in competition with the flood.
	h := newFloodHarness(ctx, t, floodHarnessArgs{validators: 2, wallClock: true})
	honestPeer := types.NodeID("honest-validator")
	other := h.vss[1]

	stateData := h.stateData()
	timeouts := types.DefaultTimeoutParams()
	require.Equal(t, timeouts.ProposeTimeout(0), stateData.proposeTimeout(0))
	require.Equal(t, timeouts.VoteTimeout(0), stateData.voteTimeout(0))
	height, round := stateData.Height, stateData.Round
	chainID := stateData.state.ChainID

	voteCh := subscribe(ctx, t, h.cs.eventBus, types.EventQueryVote)
	newBlockCh := subscribe(ctx, t, h.cs.eventBus, types.EventQueryNewBlock)
	newRoundCh := subscribe(ctx, t, h.cs.eventBus, types.EventQueryNewRound)

	// The flood starts before the height does and runs until the height is
	// over, so there is no quiet window for the honest votes to arrive in.
	stopFlood := h.sustainFlood(ctx, t, maxConnectionSlots-1)
	defer stopFlood()

	// Drain after seeding so setup time cannot refill the initial burst.
	drainVerificationBudget(h.inner)

	started := time.Now()
	deadline := started.Add(heightProgressDeadline)
	nextMessage := func(ch <-chan tmpubsub.Message) tmpubsub.Message {
		t.Helper()
		return ensureMessageBeforeTimeout(t, ch, time.Until(deadline))
	}
	checkRound := func(height int64, round int32) {
		t.Helper()
		event, ok := nextMessage(newRoundCh).Data().(types.EventDataNewRound)
		require.True(t, ok)
		require.Equal(t, height, event.Height)
		require.Equal(t, round, event.Round)
	}
	checkVote := func(voteType tmproto.SignedMsgType, blockID *types.BlockID) *types.Vote {
		t.Helper()
		event, ok := nextMessage(voteCh).Data().(types.EventDataVote)
		require.True(t, ok)
		vote := event.Vote
		require.Equal(t, height, vote.Height)
		require.Equal(t, round, vote.Round)
		require.Equal(t, voteType, vote.Type)
		if blockID != nil {
			require.Equal(t, *blockID, vote.BlockID)
		}
		return vote
	}
	startTestRound(ctx, h.cs, height, round)
	checkRound(height, round)
	ownPrevote := checkVote(tmproto.PrevoteType, nil)
	require.Equal(t, h.vss[0].Index, ownPrevote.ValidatorIndex)

	rs := h.cs.GetRoundState()
	blockID := rs.BlockID()
	require.False(t, blockID.IsNil(), "the node did not get a block proposed under the flood")
	require.Equal(t, blockID, ownPrevote.BlockID, "the node did not prevote for its proposed block")

	// Establish contention before honest votes.
	require.Eventually(t, func() bool { return h.budgetWaits.count() > 0 }, time.Until(deadline), time.Millisecond,
		"the verification budget was never contended, so the flood did not load the node")

	// The other validator's prevote, arriving over its own lane like any peer's.
	honestArrived := time.Now()
	h.sendFromPeer(ctx, t, honestPeer, signVotes(ctx, t, tmproto.PrevoteType, chainID, blockID,
		stateData.AppHash, stateData.Validators.QuorumType, stateData.Validators.QuorumHash, other)...)
	peerPrevote := checkVote(tmproto.PrevoteType, &blockID)
	require.Equal(t, other.Index, peerPrevote.ValidatorIndex)
	prevoteLatency := time.Since(honestArrived)

	ownPrecommit := checkVote(tmproto.PrecommitType, &blockID)
	require.Equal(t, h.vss[0].Index, ownPrecommit.ValidatorIndex)

	// And its precommit, the expensive message of the round.
	h.sendFromPeer(ctx, t, honestPeer, signVotes(ctx, t, tmproto.PrecommitType, chainID, blockID,
		stateData.AppHash, stateData.Validators.QuorumType, stateData.Validators.QuorumHash, other)...)

	blockEvent, ok := nextMessage(newBlockCh).Data().(types.EventDataNewBlock)
	require.True(t, ok)
	require.Equal(t, height, blockEvent.Block.Height)
	checkRound(height+1, 0)
	elapsed := time.Since(started)

	reportf(t, "height %d finished in %s under %d/%d flooded lanes; honest prevote served in %s",
		height, elapsed, maxConnectionSlots-1, maxConnectionSlots, prevoteLatency)
	reportf(t, "the flood offered %d messages and had %.0f shed; %d work charged in total",
		h.floodOffered(), h.laneDrops.count(), h.chargedWork())

	require.Less(t, elapsed, heightProgressDeadline,
		"the node did not finish the height while a peer flooded it")
	require.Empty(t, h.cs.peerErrorQueue.ch,
		"a flood of unverifiable votes must not be reported as its senders' fault")

	// Token waits prove contention even when preemption limits flood throughput.
	reportf(t, "the verification budget waited for tokens %.0f times", h.budgetWaits.count())
	charged, allowed := float64(h.chargedWork()), budgetAllowance(verificationRate, elapsed)
	require.LessOrEqual(t, charged, allowed,
		"more verification work was charged than the node-wide budget allows")
}

// sendFromPeer delivers votes over a lane of their own, the way a peer's votes
// arrive. It is what distinguishes this from the ordinary state tests, which
// inject votes with no sender and so are never scheduled or charged for.
func (h *floodHarness) sendFromPeer(
	ctx context.Context,
	t *testing.T,
	peerID types.NodeID,
	votes ...*types.Vote,
) {
	t.Helper()
	for _, vote := range votes {
		require.NoError(t, h.cs.msgInfoQueue.send(ctx, &VoteMessage{Vote: vote}, peerID))
	}
}

// sustainFlood seeds each lane with an unverifiable precommit, then supplies
// unverifiable prevotes until the returned function is called.
//
// A flood that is queued once and then drains leaves a quiet window behind it,
// and an honest message arriving in that window measures nothing. Topping the
// lanes up for as long as the test runs is what keeps the node under load for
// the whole height.
func (h *floodHarness) sustainFlood(ctx context.Context, t *testing.T, lanes int) func() {
	t.Helper()
	// Deep enough that the lanes never run dry between top-ups, shallow enough
	// that a top-up is not itself a long pause.
	const perLane = 8

	// A maximum-cost head makes each lane compete for whole-message budget,
	// even when CPU contention slows the subsequent one-signature prevotes.
	head := unsignedPrecommit(ctx, t, h, types.MaxVoteExtensions)
	head.BlockSignature = wellFormedWrongSignature(ctx, t, h)
	for i := 0; i < lanes; i++ {
		h.sendFromPeer(ctx, t, attackerID(i), head.Copy())
	}
	h.offered.Add(int64(lanes))

	// Populate every attacker lane before consensus starts.
	h.floodPrevotes(ctx, t, lanes, perLane)
	h.offered.Add(int64(lanes * perLane))

	vote := unsignedPrevote(ctx, t, h)
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			for i := 0; i < lanes; i++ {
				peerID := attackerID(i)
				for j := 0; j < perLane; j++ {
					if h.cs.msgInfoQueue.send(ctx, &VoteMessage{Vote: vote.Copy()}, peerID) != nil {
						return
					}
					h.offered.Add(1)
				}
			}
			// Let the node work through some of it before topping up, so the
			// flood is a sustained arrival rate rather than one enormous burst.
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func (h *floodHarness) floodOffered() int64 { return h.offered.Load() }

// offeredCounter counts what a background flood has sent, for a test goroutine
// to read while the flood is still running.
type offeredCounter struct {
	mtx   sync.Mutex
	value int64
}

func (c *offeredCounter) Add(n int64) {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	c.value += n
}

func (c *offeredCounter) Load() int64 {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	return c.value
}
