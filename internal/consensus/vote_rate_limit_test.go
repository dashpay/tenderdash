package consensus

import (
	"context"
	"fmt"
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/client"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func newRateLimitedReactor(ctx context.Context, limit float64, opts ...client.RateLimitOptionFunc) *Reactor {
	return &Reactor{
		logger: log.NewNopLogger(),
		voteRateLimit: client.NewRateLimitWithBurst(ctx, limit, voteRateBurst,
			true, log.NewNopLogger(), opts...),
	}
}

func voteEnvelope(from types.NodeID) *p2p.Envelope {
	return &p2p.Envelope{
		From:      from,
		ChannelID: p2p.ConsensusVoteChannel,
		Message:   testVoteMsg(tmproto.PrevoteType, testBlockID(), 0),
	}
}

func precommitEnvelope(from types.NodeID, nExtensions int) *p2p.Envelope {
	return &p2p.Envelope{
		From:      from,
		ChannelID: p2p.ConsensusVoteChannel,
		Message:   testVoteMsg(tmproto.PrecommitType, testBlockID(), nExtensions),
	}
}

// A single peer flooding the vote channel must have its excess dropped once it
// exceeds its per-peer budget, so it cannot force unbounded signature
// verification.
func TestAllowVoteChannelMessage_DropsFloodFromSinglePeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const limit = 5.0
	r := newRateLimitedReactor(ctx, limit)

	// More than the bucket can ever hold, so the flood must run into the limit
	// whatever the peer sends.
	const n = 2 * voteRateBurst
	allowed, dropped := 0, 0
	for i := 0; i < n; i++ {
		if r.allowVoteChannelMessage(ctx, voteEnvelope("attacker")) {
			allowed++
		} else {
			dropped++
		}
	}

	assert.Positive(t, dropped, "a burst well above the budget must have messages dropped")
	assert.LessOrEqual(t, allowed, voteRateBurst+1,
		"allowed count is bounded by the burst, not the flood size")
	assert.Equal(t, n, allowed+dropped)
}

// The limit is per peer: one peer exhausting its budget must not cause an
// honest peer's messages to be dropped.
func TestAllowVoteChannelMessage_PerPeerIndependent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := newRateLimitedReactor(ctx, 5)

	// Exhaust the attacker's budget.
	for i := 0; i < 2*voteRateBurst; i++ {
		r.allowVoteChannelMessage(ctx, voteEnvelope("attacker"))
	}

	// An honest peer sending a modest number is unaffected.
	for i := 0; i < 10; i++ {
		assert.True(t, r.allowVoteChannelMessage(ctx, voteEnvelope("honest")),
			"an honest peer under its own budget must not be dropped")
	}
}

// Only the vote channel is rate limited; other channels always pass.
func TestAllowVoteChannelMessage_OtherChannelsNotLimited(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := newRateLimitedReactor(ctx, 5)

	for i := 0; i < 500; i++ {
		env := &p2p.Envelope{From: "peer", ChannelID: p2p.ConsensusStateChannel}
		assert.True(t, r.allowVoteChannelMessage(ctx, env), "non-vote channels are not rate limited")
	}
}

// The budget is denominated in verification work, not in messages: a peer
// sending the most expensive precommit it can construct must exhaust its budget
// far sooner than one sending prevotes. Charging one token per message lets a
// peer buy 66x the CPU for the same price, which is the flood this limit exists
// to bound.
func TestAllowVoteChannelMessage_ChargesVerificationCost(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	admitted := func(env *p2p.Envelope) int {
		r := newRateLimitedReactor(ctx, 600)
		allowed := 0
		for i := 0; i < 5000; i++ {
			if r.allowVoteChannelMessage(ctx, env) {
				allowed++
			}
		}
		return allowed
	}

	prevotes := admitted(voteEnvelope("prevoter"))
	precommits := admitted(precommitEnvelope("precommitter", types.MaxVoteExtensions))

	assert.Greater(t, prevotes, precommits*10,
		"an expensive precommit must consume far more budget than a prevote")

	// The bucket starts full, so an instantaneous flood admits at most
	// burst/cost messages of that cost.
	assert.LessOrEqual(t, precommits, voteRateBurst/maxPeerMessageCost+1,
		"maximum-cost precommits admitted must be bounded by burst/cost")
}

// A peer that spends its budget on expensive precommits must not thereby buy
// itself extra cheap messages: the two draw on the same token bucket.
func TestAllowVoteChannelMessage_CostSharesOneBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const limit = 600.0
	r := newRateLimitedReactor(ctx, limit)

	// Drain the bucket with maximum-cost precommits.
	for i := 0; i < voteRateBurst/maxPeerMessageCost; i++ {
		require.True(t, r.allowVoteChannelMessage(ctx, precommitEnvelope("peer", types.MaxVoteExtensions)),
			"precommit %d must fit in a full bucket", i)
	}

	// Whatever is left is less than one more maximum-cost precommit.
	assert.False(t, r.allowVoteChannelMessage(ctx, precommitEnvelope("peer", types.MaxVoteExtensions)),
		"a drained bucket must not admit another maximum-cost precommit")
}

// A zero limit disables rate limiting entirely.
func TestAllowVoteChannelMessage_DisabledWhenZero(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := newRateLimitedReactor(ctx, 0)

	for i := 0; i < 5000; i++ {
		assert.True(t, r.allowVoteChannelMessage(ctx, voteEnvelope("peer")),
			"a zero limit must not drop anything")
	}
}

// The burst must never fall below the cost of the most expensive message the
// protocol allows. rate.Limiter rejects any request larger than the burst no
// matter how long it waits, so a low configured limit would make a
// fully-extended precommit permanently invisible — every validator's precommits
// silently dropped and the chain unable to commit.
func TestVoteRateBurstAdmitsMostExpensiveMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.GreaterOrEqual(t, voteRateBurst, maxPeerMessageCost,
		"the burst is below the most expensive message the protocol permits")

	// Tiny limits are the ones an operator is most likely to get wrong; none of
	// them may make the message class disappear.
	for _, limit := range []float64{0.5, 1, 2, 10, 32, 33, 600} {
		r := newRateLimitedReactor(ctx, limit)
		assert.True(t, r.allowVoteChannelMessage(ctx, precommitEnvelope("peer", types.MaxVoteExtensions)),
			"a maximum-cost precommit must be admissible at limit=%v", limit)
	}
}

// The burst is the work a peer may front-load before the sustained rate has any
// say, and every fresh identity starts with a full bucket. It must therefore be
// sized from honest catch-up demand alone: derived from the rate instead, every
// increase of the sustained allowance also multiplies what one identity can dump
// in a single instant.
func TestVoteRateBurstDoesNotScaleWithLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A frozen clock refills nothing, so what gets through is exactly the
	// bucket the peer started with.
	frontLoad := func(limit float64) int {
		r := newRateLimitedReactor(ctx, limit, client.WithRateLimitClock(clockwork.NewFakeClock()))
		admitted := 0
		for i := 0; i < 20000; i++ {
			if r.allowVoteChannelMessage(ctx, voteEnvelope("peer")) {
				admitted++
			}
		}
		return admitted
	}

	assert.Equal(t, frontLoad(600), frontLoad(6000),
		"a tenfold sustained rate must not buy a tenfold instantaneous burst")
	assert.Equal(t, frontLoad(600), frontLoad(60),
		"lowering the sustained rate must not shrink the catch-up allowance")
}

// Decoupling the burst from the rate must not shrink it below what an honest
// peer legitimately delivers in a catch-up burst: on the order of ten
// vote-channel messages a second, each up to the five work units a Dash precommit
// carrying four vote extensions costs.
func TestVoteRateBurstCoversHonestCatchUp(t *testing.T) {
	const (
		honestMessagesPerSecond = 10
		dashPrecommitCost       = baseMessageCost + 4
	)

	assert.GreaterOrEqual(t, voteRateBurst, honestMessagesPerSecond*dashPrecommitCost,
		"the burst must absorb at least a second of an honest peer's heaviest gossip")
}

// Every fresh identity starts with a full bucket, so the burst is also what one
// reconnect — or one more occupied connection slot — buys instantly. Across the
// default connection ceiling that aggregate must stay below the shared consensus
// message queue: above it, peers that have sent nothing before could fill the
// queue from their bursts alone, and the sustained rate never gets a say.
func TestVoteRateBurstBoundsFreshPeerAggregate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := newRateLimitedReactor(ctx, config.DefaultConsensusConfig().PeerVoteRateLimit,
		client.WithRateLimitClock(clockwork.NewFakeClock()))

	// The ceiling is not MaxConnections: the peer manager may hold that many
	// connections AND a fixed allowance of extra ones while it upgrades away
	// lower-scored peers (node/setup.go passes 4 as MaxConnectedUpgrade), and
	// every one of those is a peer that can send.
	const connectedUpgradeAllowance = 4
	peers := int(config.DefaultP2PConfig().MaxConnections) + connectedUpgradeAllowance
	admitted := 0
	for p := 0; p < peers; p++ {
		peer := types.NodeID(fmt.Sprintf("fresh-peer-%d", p))
		for r.allowVoteChannelMessage(ctx, voteEnvelope(peer)) {
			admitted++
		}
	}

	assert.Less(t, admitted, msgQueueSize,
		"%d fresh peers must not be able to fill the %d-slot consensus queue from burst alone",
		peers, msgQueueSize)
}

// Rejecting an over-long extension list must stay a local drop. Turning it into
// a peer error would let a version skew — or a bug in the cost model — evict
// honest peers, and eviction is the one outcome an attacker can turn against
// the network.
func TestAllowVoteChannelMessage_UnpriceableMessageDroppedLocally(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := newRateLimitedReactor(ctx, 600)

	assert.False(t, r.allowVoteChannelMessage(ctx, precommitEnvelope("peer", types.MaxVoteExtensions+1)),
		"a message that cannot be priced must be dropped before verification")

	// A message type the cost model does not know is dropped the same quiet
	// way, for the same reason.
	unknownType := &p2p.Envelope{
		From:      "peer",
		ChannelID: p2p.ConsensusVoteChannel,
		Message:   &tmproto.Vote{},
	}
	assert.False(t, r.allowVoteChannelMessage(ctx, unknownType),
		"a message type with no price must be dropped before verification")

	// Dropping it must not have cost the peer its budget either: the drop is a
	// local decision, not a charge.
	for i := 0; i < 10; i++ {
		assert.True(t, r.allowVoteChannelMessage(ctx, voteEnvelope("peer")),
			"an unpriceable message must not consume the peer's budget")
	}
}
