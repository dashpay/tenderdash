package consensus

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/types"
)

// honestServiceBound is what this node promises a continuously connected peer,
// registered here rather than derived from a run.
//
// A bound read off a measurement is not a bound: it passes whatever the code
// happens to do. These are the round timeouts the protocol itself works to, so
// meeting them is what "the round this message belongs to can still finish"
// means, and the numbers below are graded against them.
//
// The two differ because equal service per identity makes the wait scale with
// the message's own cost. A prevote is one signature check and is expected
// inside the vote timeout. The heaviest precommit Dash validators produce is
// ten, and is only expected inside the propose timeout: fair service for a head
// that expensive takes longer than the vote timeout allows, so the round it
// belongs to is lost and the next one carries it.
type honestServiceBound struct {
	// extensions is the honest head's vote-extension count; a negative count
	// asks for a prevote.
	extensions int
	// work is what verifying that head costs, from the shipped cost model.
	work int
	// deadline is the round timeout the head must beat.
	deadline time.Duration
}

func honestServiceBounds() []honestServiceBound {
	timeouts := types.DefaultTimeoutParams()
	return []honestServiceBound{
		{extensions: -1, work: baseMessageCost, deadline: timeouts.Vote},
		{extensions: 4, work: baseMessageCost + 4, deadline: timeouts.Propose},
	}
}

// attackerLaneSweep is the share of the node's connection slots an attacker
// holds, from none to all but one.
//
// The endpoints alone do not answer the question an operator has, which is how
// much of the node an attacker has to own before honest service stops being
// good enough. The intermediate points are what locate that, and 50 is what is
// left once the connection-slot reservation guarantees 18 honest slots of 68.
var attackerLaneSweep = []int{0, 8, 16, 30, 40, 50, 60, maxConnectionSlots - 1}

// A peer that stays connected must be served whatever the other peers send.
// This sweeps how many of the node's connection slots the attacker holds and
// records what an honest peer's message then costs in delay, so the point where
// honest service stops meeting a round timeout can be read off rather than
// guessed at.
//
// The flood is the one an attacker would actually run: prevotes that cannot
// verify. A prevote is the cheapest message that still forces a signature
// check, so every work unit it takes from this node is one the sender produced
// for free — and, unlike a forged commit, it is not attributable, so sending it
// costs the attacker nothing at the connection level either.
func TestLoadHonestServiceUnderSybilFlood(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	timeouts := types.DefaultTimeoutParams()

	for _, bound := range honestServiceBounds() {
		t.Run(fmt.Sprintf("honest head costing %d", bound.work), func(t *testing.T) {
			// voteCliff is the smallest number of attacker lanes at which the
			// honest head no longer makes the vote timeout. Reporting it is the
			// point of sweeping rather than testing the endpoints: it says how
			// much of this node an attacker has to own before honest service
			// degrades, and it is the number that moves if an input changes.
			voteCliff := -1
			for _, attackers := range attackerLaneSweep {
				latency := measureHonestLatency(t, attackers, bound)
				reportf(t, "honest head of cost %d under %d/%d attacker lanes completed in %s "+
					"(vote timeout %s: %s, propose timeout %s: %s)",
					bound.work, attackers, maxConnectionSlots, latency,
					timeouts.Vote, met(latency < timeouts.Vote),
					timeouts.Propose, met(latency < timeouts.Propose))
				if voteCliff < 0 && latency >= timeouts.Vote {
					voteCliff = attackers
				}
				require.Less(t, latency, bound.deadline,
					"an honest peer's message missed the round it belongs to")
			}
			if voteCliff < 0 {
				reportf(t, "a head of cost %d meets the vote timeout at every lane ratio swept", bound.work)
			} else {
				reportf(t, "a head of cost %d stops meeting the vote timeout at %d/%d attacker lanes",
					bound.work, voteCliff, maxConnectionSlots)
			}
		})
	}
}

func met(ok bool) string {
	if ok {
		return "met"
	}
	return "missed"
}

// measureHonestLatency returns how long one honest peer's message waits for
// verification while the attacker holds the given number of lanes saturated.
//
// The clock advances only while something waits for verification budget, and
// only as far as the next whole token, so the result is the delay the budget
// and the rotation imposed between them and nothing else — in particular not
// the speed of the machine this runs on.
func measureHonestLatency(t *testing.T, attackers int, bound honestServiceBound) time.Duration {
	t.Helper()
	return measureHonestLatencyAgainst(t, attackers, bound, (*floodHarness).floodPrevotes)
}

// floodFunc fills the given number of lanes with the given number of messages
// each.
type floodFunc func(h *floodHarness, ctx context.Context, t *testing.T, lanes, perLane int)

// measureHonestLatencyAgainst is measureHonestLatency against a chosen flood,
// so the same measurement can be taken of the message shapes a peer can send.
func measureHonestLatencyAgainst(
	t *testing.T,
	attackers int,
	bound honestServiceBound,
	flood floodFunc,
) time.Duration {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	h := newFloodHarness(ctx, t, floodHarnessArgs{validators: 4})
	stateData := h.stateData()

	// Start with the budget spent, so the flood is paid for out of the refill
	// rate and really does compete with the honest peer for it.
	drainVerificationBudget(h.inner)
	start := h.clock.Now()

	// Enough per lane that no attacker runs dry before the honest head is
	// served, so nothing but the rotation limits what they get through.
	const perLane = 32
	flood(h, ctx, t, attackers, perLane)

	stop := h.start(ctx)
	defer stop()

	if attackers > 0 {
		require.Eventually(t, func() bool { return len(h.budget.charges()) > 0 },
			30*time.Second, time.Millisecond, "the flood never started")
		// The flood is only the flood this test claims to run if each of its
		// messages really is as cheap as it looks. Read before the honest
		// message arrives, so only the flood's own charges are counted.
		assertFloodSettlesAtBaseCost(t, h.budget.charges())
	}

	// The honest message arrives once the flood is already being verified, so
	// it waits behind a turn already in flight as well as behind the rotation.
	honest := honestVote(ctx, t, h.vss[1], &stateData, bound.extensions)
	require.Equal(t, bound.work, laneTurnCost(msgInfo{Msg: &VoteMessage{Vote: honest}}),
		"the honest head does not cost what this case is about")
	arrived := h.clock.Now()
	require.NoError(t, h.cs.msgInfoQueue.send(ctx, &VoteMessage{Vote: honest}, "honest"))

	require.Eventually(t, func() bool { return voteAccepted(h.cs, honest) },
		60*time.Second, time.Millisecond,
		"the honest peer's vote was never verified while the attacker flooded")
	latency := h.clock.Now().Sub(arrived)

	// The flood has to have outlasted the honest message, or what was measured
	// is a wait that ended when the attacker ran out rather than when the
	// rotation came round.
	if attackers > 0 {
		require.Positive(t, h.cs.msgInfoQueue.lanes.buffered(),
			"the attacker ran dry before the honest message was served, so the latency "+
				"above is not the latency under a sustained flood")
	}

	// Equal service per identity says the honest head waits for every other
	// lane to be granted its own cost, and no longer. Graded rather than
	// reported, because at the cheap end the round timeouts are far enough away
	// that no flood this node can receive could miss them — a deadline alone
	// would pass whatever the scheduler did.
	expected := float64(attackers*bound.work) / verificationRate
	require.InDelta(t, expected, latency.Seconds(), (expected+1)/3,
		"the honest head waited for something other than one turn of the rotation "+
			"per unit of its own cost")

	// Everything the run charged stayed inside the envelope the budget was
	// built for, or the latency above was bought by spending more CPU than the
	// node is allowed to.
	assertNoWorkAboveBudget(t, h, verificationRate, h.clock.Now().Sub(start))
	return latency
}

// What honest operation alone costs decides whether the budget binds only under
// attack or all the time. This measures the verification work one round of
// honest votes charges, per validator, so the figure can be read against the
// budget for a quorum of any size.
//
// The per-validator figure is what matters, and it is exact rather than
// extrapolated: the cost model prices each vote independently of how many
// others there are, so a quorum's demand is the per-validator cost times the
// quorum size.
func TestLoadHonestRoundDemandAgainstBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	// What Dash validators actually produce: a prevote carrying nothing, then a
	// precommit carrying the four threshold vote extensions of a real round.
	const extensions = 4

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	h := newFloodHarness(ctx, t, floodHarnessArgs{validators: 4})
	stateData := h.stateData()

	perValidator := 0
	for _, msg := range []Message{
		&VoteMessage{Vote: honestVote(ctx, t, h.vss[1], &stateData, -1)},
		&VoteMessage{Vote: honestVote(ctx, t, h.vss[2], &stateData, extensions)},
	} {
		cost, err := budgetedMessageCost(msg)
		require.NoError(t, err)
		perValidator += cost
	}

	reportf(t, "one honest validator's votes cost %d work per round (prevote + %d-extension precommit)",
		perValidator, extensions)

	for _, quorum := range []int{4, 64, 100} {
		demand := perValidator * quorum
		seconds := float64(demand) / verificationRate
		reportf(t, "a %d-validator quorum demands %d work per round: %.2fs of a %.0f work/s budget "+
			"(propose timeout %s, vote timeout %s)",
			quorum, demand, seconds, float64(verificationRate),
			types.DefaultTimeoutParams().Propose, types.DefaultTimeoutParams().Vote)
	}

	// The check that matters is not the size of the number but whether the
	// budget can absorb one round's honest demand inside the round. Anything
	// else means the budget binds in normal operation and not only under
	// attack.
	roundBudget := types.DefaultTimeoutParams().Propose + types.DefaultTimeoutParams().Vote
	sustainable := int(verificationRate * roundBudget.Seconds() / float64(perValidator))
	reportf(t, "the budget sustains an all-honest round for a quorum of up to %d validators "+
		"within propose+vote (%s)", sustainable, roundBudget)

	require.GreaterOrEqual(t, sustainable, dashQuorumSize,
		"the verification budget can no longer absorb one round of honest votes from a "+
			"full quorum inside propose+vote, so it now binds in normal operation and not "+
			"only under attack: either the per-vote cost has risen or the rate has fallen")
}

// dashQuorumSize is the validator set the budget has to serve: Dash runs
// consensus over a hundred-node quorum drawn from the masternode list.
const dashQuorumSize = 100
