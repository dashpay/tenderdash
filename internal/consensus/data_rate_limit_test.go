package consensus

import (
	"context"
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/client"
	"github.com/dashpay/tenderdash/libs/log"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	"github.com/dashpay/tenderdash/types"
)

// The limiter meters against a frozen clock so no tokens refill while a test
// runs. Refill is a function of elapsed wall time, which on a loaded machine
// admits more messages than the burst alone allows and makes any assertion on
// the number admitted depend on how fast the test happens to run.
func newDataRateLimitedReactor(ctx context.Context, limit float64) *Reactor {
	return &Reactor{
		logger: log.NewNopLogger(),
		dataRateLimit: client.NewRateLimitWithBurst(ctx, limit, dataRateBurstFor(limit),
			true, log.NewNopLogger(), client.WithRateLimitClock(clockwork.NewFakeClock())),
	}
}

func blockPartEnvelope(from types.NodeID) *p2p.Envelope {
	return &p2p.Envelope{
		From:      from,
		ChannelID: p2p.ConsensusDataChannel,
		Message:   &tmcons.BlockPart{},
	}
}

func proposalEnvelope(from types.NodeID) *p2p.Envelope {
	return &p2p.Envelope{
		From:      from,
		ChannelID: p2p.ConsensusDataChannel,
		Message:   &tmcons.Proposal{},
	}
}

// A proposal flood must be cut off well before it can force unbounded BLS
// verifications. A rejected proposal never becomes rs.Proposal, so nothing
// deduplicates the flood — the rate limit is the only bound.
func TestAllowDataChannelMessage_DropsProposalFlood(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const limit = 500.0
	r := newDataRateLimitedReactor(ctx, limit)

	const n = 5000
	allowed, dropped := 0, 0
	for i := 0; i < n; i++ {
		if r.allowDataChannelMessage(ctx, proposalEnvelope("attacker")) {
			allowed++
		} else {
			dropped++
		}
	}

	assert.Equal(t, n, allowed+dropped)
	assert.Positive(t, dropped, "a proposal flood far above the budget must be dropped")

	// The bucket starts full at burst tokens and each proposal costs
	// proposalTokenCost, so an instantaneous flood admits at most
	// burst/proposalTokenCost proposals.
	maxAdmissible := dataRateBurstFor(limit)/proposalTokenCost + 1
	assert.LessOrEqual(t, allowed, maxAdmissible,
		"proposals admitted must be bounded by burst/cost, not by the flood size")
}

// Block-part gossip must not be throttled: non-validators legitimately receive
// parts to catch up, and dropping them stalls sync (parts are never resent).
// An honest peer's realistic burst has to pass untouched.
func TestAllowDataChannelMessage_HonestBlockPartBurstNotDropped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := newDataRateLimitedReactor(ctx, 500)

	// The gossiper emits at most one data message per PeerGossipSleepDuration
	// per peer. At the 5ms used by test configs that is 200/s — the fastest
	// honest rate this repo configures anywhere. One second of that must survive.
	const honestBurst = 200
	for i := 0; i < honestBurst; i++ {
		require.True(t, r.allowDataChannelMessage(ctx, blockPartEnvelope("honest")),
			"honest block-part gossip must not be dropped (message %d)", i)
	}
}

// A peer saturating the data channel must not consume another peer's budget.
func TestAllowDataChannelMessage_PerPeerIndependent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := newDataRateLimitedReactor(ctx, 500)

	for i := 0; i < 5000; i++ {
		r.allowDataChannelMessage(ctx, proposalEnvelope("attacker"))
	}

	for i := 0; i < 200; i++ {
		assert.True(t, r.allowDataChannelMessage(ctx, blockPartEnvelope("honest")),
			"an honest peer under its own budget must not be dropped")
	}
}

// Proposals are charged more than block parts, so the same number of proposals
// exhausts the budget sooner. This is what lets the limit stay generous for
// parts while still bounding verification work.
func TestAllowDataChannelMessage_ProposalsCostMoreThanBlockParts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	count := func(env func(types.NodeID) *p2p.Envelope, peer types.NodeID) int {
		r := newDataRateLimitedReactor(ctx, 500)
		allowed := 0
		for i := 0; i < 5000; i++ {
			if r.allowDataChannelMessage(ctx, env(peer)) {
				allowed++
			}
		}
		return allowed
	}

	parts := count(blockPartEnvelope, "peer-parts")
	proposals := count(proposalEnvelope, "peer-proposals")

	assert.Greater(t, parts, proposals,
		"block parts must be cheaper than proposals, otherwise catch-up gossip is throttled to protect against proposals")
}

// Only the data channel is limited here; the vote channel has its own limiter
// and the state channel is unlimited.
func TestAllowDataChannelMessage_OtherChannelsNotLimited(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := newDataRateLimitedReactor(ctx, 1)

	for i := 0; i < 500; i++ {
		env := &p2p.Envelope{From: "peer", ChannelID: p2p.ConsensusStateChannel}
		assert.True(t, r.allowDataChannelMessage(ctx, env), "non-data channels are not rate limited here")
	}
}

// A zero limit disables data-channel rate limiting entirely.
func TestAllowDataChannelMessage_DisabledWhenZero(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := newDataRateLimitedReactor(ctx, 0)

	for i := 0; i < 5000; i++ {
		assert.True(t, r.allowDataChannelMessage(ctx, proposalEnvelope("peer")),
			"a zero limit must not drop anything")
	}
}

// The burst must exceed the cost of the single most expensive message. If it did
// not, rate.Limiter.AllowN would reject that message permanently and the node
// would never admit a proposal — an availability failure worse than the flood.
func TestDataRateBurstAdmitsMostExpensiveMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Even at an implausibly low configured limit, one proposal must get through.
	for _, limit := range []float64{1, 2, 10, 500} {
		r := newDataRateLimitedReactor(ctx, limit)
		assert.True(t, r.allowDataChannelMessage(ctx, proposalEnvelope("peer")),
			"a proposal must be admissible at limit=%v, otherwise proposals are permanently invisible", limit)
	}
}
