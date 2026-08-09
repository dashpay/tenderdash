package consensus

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/types"
)

// A round does not finish when one honest vote is verified. It finishes when
// two thirds of them are, and equal service per identity makes those two
// numbers behave very differently under a flood.
//
// Serving one honest head of cost W while the attacker holds A lanes costs the
// budget A·W, because each attacker lane is granted a quantum for every quantum
// the honest head accumulates. Serving one head from each of H honest lanes
// costs (H+A)·W and delivers H votes, so a quorum of Q validators needs
// ceil(2Q/3 / H) of those rotations. The single-message latency the fairness
// sweep records is that divided by the number of honest lanes — which is the
// figure that matters only when there is one vote to wait for.
//
// This measures the whole quorum, because that is what a round waits for.
func TestLoadQuorumFormationUnderFlood(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	// Small enough that generating the quorum's keys does not dominate the run.
	// The rotation is linear in the votes it carries, and the honest-lane
	// counts below are what make that linearity measured rather than assumed.
	const validators = 24

	testCases := []struct {
		name       string
		extensions int
		// honestLanes is how many of the node's connection slots carry honest
		// traffic; every other slot is the attacker's.
		honestLanes int
		// deadline is the round timeout this quorum must form inside. Zero
		// means the round timeout is not the right grade for the case — it is
		// still graded, against the rotation cost below.
		deadline time.Duration
	}{
		// The prevote round: one signature check per vote.
		{name: "prevotes", extensions: -1, honestLanes: 18,
			deadline: types.DefaultTimeoutParams().Vote},
		// The precommit round, as Dash validators actually vote: four threshold
		// vote extensions, verified twice.
		{name: "precommits with four extensions", extensions: 4, honestLanes: 18,
			deadline: types.DefaultTimeoutParams().Propose},
		// Fewer honest lanes than there are votes to carry, so the rotation has
		// to run several times over. This is what a full Dash quorum looks like
		// against the honest slots the connection reservation guarantees, and
		// it is here to show the cost really is the rotation repeated rather
		// than a single rotation however many votes are waiting.
		{name: "precommits across too few honest lanes", extensions: 4, honestLanes: 6},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			honestLanes := tc.honestLanes
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			h := newFloodHarness(ctx, t, floodHarnessArgs{validators: validators})
			stateData := h.stateData()
			attackerLanes := maxConnectionSlots - honestLanes

			// One vote from every validator but this node's own, spread across
			// the honest lanes the way peer relaying spreads them.
			votes := make([]*types.Vote, 0, validators-1)
			for i := 1; i < validators; i++ {
				votes = append(votes, honestVote(ctx, t, h.vss[i], &stateData, tc.extensions))
			}
			perVote := laneTurnCost(msgInfo{Msg: &VoteMessage{Vote: votes[0]}})
			needed := len(stateData.Validators.Validators)*2/3 + 1

			drainVerificationBudget(h.inner)
			start := h.clock.Now()
			h.floodPrevotes(ctx, t, attackerLanes, 64)
			for i, vote := range votes {
				lane := types.NodeID(fmt.Sprintf("honest-%02d", i%honestLanes))
				require.NoError(t, h.cs.msgInfoQueue.send(ctx, &VoteMessage{Vote: vote}, lane))
			}

			stop := h.start(ctx)
			defer stop()

			require.Eventually(t, func() bool { return acceptedVotes(h.cs, votes) >= needed },
				2*time.Minute, time.Millisecond,
				"two thirds of the honest votes were never verified while the attacker flooded")
			elapsed := h.clock.Now().Sub(start)

			timeouts := types.DefaultTimeoutParams()
			rotations := (needed + honestLanes - 1) / honestLanes
			reportf(t, "%d of %d honest votes of cost %d verified in %s across %d honest lanes "+
				"against %d attacker lanes in %d rotation(s) of %d work "+
				"(vote timeout %s: %s, propose timeout %s: %s)",
				needed, len(votes), perVote, elapsed, honestLanes, attackerLanes,
				rotations, (honestLanes+attackerLanes)*perVote,
				timeouts.Vote, met(elapsed < timeouts.Vote),
				timeouts.Propose, met(elapsed < timeouts.Propose))

			// What the same rotation costs a full Dash quorum, which is the
			// number an operator needs and which no fixture of this size can
			// reach directly. The rotation is linear in the votes it carries.
			for _, quorum := range []int{64, 100} {
				rotations := (quorum*2/3 + 1 + honestLanes - 1) / honestLanes
				work := rotations * (honestLanes + attackerLanes) * perVote
				reportf(t, "a %d-validator quorum needs %d rotations of %d work: %.2fs at %.0f work/s",
					quorum, rotations, (honestLanes+attackerLanes)*perVote,
					float64(work)/verificationRate, float64(verificationRate))
			}

			assertNoWorkAboveBudget(t, h, verificationRate, h.clock.Now().Sub(start))
			if tc.deadline > 0 {
				require.Less(t, elapsed, tc.deadline,
					"the round could not gather two thirds of its votes in time")
			}

			// Graded against the rotation whatever the deadline says, so no
			// case is merely reported. Equal service per identity says the
			// quorum costs one rotation per batch of honest lanes and no more;
			// a scheduler that let attacker lanes take more than their turn
			// would run long, and one that starved them would run short and be
			// no longer fair.
			expected := float64(rotations*(honestLanes+attackerLanes)*perVote) / verificationRate
			require.InDelta(t, expected, elapsed.Seconds(), expected/4,
				"gathering two thirds of the votes cost something other than the rotation "+
					"the scheduler's fairness implies")
		})
	}
}

// Nothing about this node's fairness rests on which message an attacker floods
// it with, and that is worth pinning rather than assuming: it is the reason
// deciding whether to accept a vote by who sent it bought nothing.
//
// Three shapes an unprivileged peer can produce for free — a precommit vote, a
// proposal and a commit, none of which it can sign — all settle at one
// verification, take one turn in the rotation, and delay an honest peer's
// message by the same amount. A defence that closed one of them would leave the
// other two open at the same price.
//
// What one message costs is not what a campaign costs, and the two answers
// differ. A commit whose threshold signature does not check out is
// attributable, because a node stores a commit only after verifying it, so a
// forged one cannot have come from an honest relayer: it costs its sender the
// connection, one message per identity. A forged vote or proposal is not
// attributable — verification failure is not proof of guilt — so its sender
// keeps the slot and sends as fast as its channel allowance permits. Those
// allowances are not equal either, and the vote channel is the cheaper of the
// two, so the shapes are interchangeable per message and are not
// interchangeable per connection slot.
//
// Both halves are measured, because either one alone reads as the whole answer
// and is not.
func TestLoadFloodShapesCostTheSameWhicheverChannelTheyArriveOn(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	bound := honestServiceBounds()[1] // the expensive head, where a gap would show
	const attackers = maxConnectionSlots - 1

	testCases := []struct {
		name  string
		flood floodFunc
		// attributable reports whether this node can tell the message was
		// forged, and so whether sending it costs the sender its connection.
		attributable bool
	}{
		{name: "precommit votes", flood: (*floodHarness).floodPrevotes},
		{name: "proposals", flood: (*floodHarness).floodForgedProposals},
		{name: "commits", flood: (*floodHarness).floodLanes, attributable: true},
	}

	latencies := make(map[string]time.Duration, len(testCases))
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			latencies[tc.name] = measureHonestLatencyAgainst(t, attackers, bound, tc.flood)
			reported := measureFloodAttribution(t, tc.flood, tc.attributable)
			reportf(t, "a flood of forged %s: honest head of cost %d delayed %s, "+
				"%d of %d messages reported their sender",
				tc.name, bound.work, latencies[tc.name], reported, attributionFloodSize)
		})
	}

	// Equal to within one turn of the rotation. Whichever shape an attacker
	// picks, the honest peer waits the same.
	rotation := float64(attackers) / float64(verificationRate)
	for name, latency := range latencies {
		require.InDelta(t, latencies["precommit votes"].Seconds(), latency.Seconds(), rotation,
			"a flood of forged %s costs this node materially less than one of forged votes, "+
				"so the vote path is not the cheapest way in after all", name)
	}

	// Per connection slot they are not equal, and the vote channel is the
	// cheaper one. This is what decides how many identities an attacker has to
	// bring to hold a given rate, so it is the number that answers whether one
	// channel is a substitute for another — the per-message equality above does
	// not.
	cfg := config.DefaultConsensusConfig()
	votesPerSlot := cfg.PeerVoteRateLimit / float64(baseMessageCost)
	proposalsPerSlot := cfg.PeerDataRateLimit / float64(proposalTokenCost)
	reportf(t, "sustained from one connection slot: %.0f forged votes/s against %.0f forged "+
		"proposals/s — the vote channel is %.0fx the cheaper of the two",
		votesPerSlot, proposalsPerSlot, votesPerSlot/proposalsPerSlot)
	require.Greater(t, votesPerSlot, proposalsPerSlot,
		"the vote channel is no longer the cheapest sustained way into the verifier; "+
			"whichever channel now is, that is the one the flood scenarios should model")
}

// attributionFloodSize is how many forged messages the attribution measurement
// sends, across four lanes.
const attributionFloodSize = 100

// measureFloodAttribution reports how many of the forged messages this node
// blamed on the peer that sent them, and fails if that does not match what the
// caller expects.
//
// Whether a message is attributable decides what it costs an attacker to keep
// the connection it is flooding from, so a change either way matters. The
// expectation is checked here rather than by the caller because the two cases
// need opposite waits: that a report arrives, and that none does.
func measureFloodAttribution(t *testing.T, flood floodFunc, attributable bool) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	h := newFloodHarness(ctx, t, floodHarnessArgs{validators: 4})
	flood(h, ctx, t, 4, attributionFloodSize/4)
	stop := h.start(ctx)
	defer stop()

	// Every message verified, which is what makes the count below a count of
	// the whole flood rather than of however much of it had been served.
	require.Eventually(t, func() bool { return len(h.budget.charges()) == attributionFloodSize },
		60*time.Second, time.Millisecond,
		"the flood was never fully served, so nothing can be counted")

	reported := func() bool { return len(h.cs.peerErrorQueue.ch) > 0 }
	if attributable {
		require.Eventually(t, reported, 30*time.Second, time.Millisecond,
			"a forged message this node can prove was forged no longer costs its sender "+
				"the connection, so flooding it has become cheaper to sustain")
	} else {
		require.Never(t, reported, 200*time.Millisecond, time.Millisecond,
			"a message that merely failed to verify was blamed on its sender; "+
				"verification failure is not proof of guilt and this evicts honest relayers")
	}
	return len(h.cs.peerErrorQueue.ch)
}

// acceptedVotes counts how many of the votes have completed verification and
// entered the vote set.
func acceptedVotes(cs *State, votes []*types.Vote) int {
	accepted := 0
	for _, vote := range votes {
		if voteAccepted(cs, vote) {
			accepted++
		}
	}
	return accepted
}

// floodForgedProposals fills lanes with proposals whose signature cannot
// verify.
//
// It is the cheapest sustained flood there is. A proposal is charged one
// verification like a vote, its only de-duplication is a field set solely by a
// proposal that passes — so every forged copy is verified again — and failing
// to verify one says nothing about the sender, so the sender keeps its
// connection.
func (h *floodHarness) floodForgedProposals(ctx context.Context, t *testing.T, lanes, perLane int) {
	t.Helper()
	stateData := h.stateData()
	for i := 0; i < lanes; i++ {
		peerID := attackerID(i)
		for j := 0; j < perLane; j++ {
			require.NoError(t, h.cs.msgInfoQueue.send(ctx,
				&ProposalMessage{Proposal: forgedProposal(&stateData)}, peerID))
		}
	}
}
