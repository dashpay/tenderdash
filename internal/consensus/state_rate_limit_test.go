package consensus

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cfg "github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/client"
	"github.com/dashpay/tenderdash/libs/bits"
	"github.com/dashpay/tenderdash/libs/log"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func newStateRateLimitedReactor(ctx context.Context, clock clockwork.Clock) *Reactor {
	r := &Reactor{
		logger:  log.NewNopLogger(),
		clock:   clock,
		Metrics: NopMetrics(),
	}
	r.stateRateLimit = client.NewRateLimitWithBurst(ctx, peerStateRateLimit, peerStateRateBurst,
		true, log.NewNopLogger(), client.WithRateLimitClock(clock))
	r.maj23PeerShare = newMaj23PeerShareLimiter(ctx, log.NewNopLogger(), client.WithRateLimitClock(clock))
	r.maj23SurplusLimit = newMaj23SurplusLimiter()
	return r
}

func hasVoteEnvelope(from types.NodeID) *p2p.Envelope {
	return &p2p.Envelope{
		From:      from,
		ChannelID: p2p.ConsensusStateChannel,
		Message:   &tmcons.HasVote{Height: 1, Round: 0, Type: tmproto.PrevoteType, Index: 0},
	}
}

func maj23Envelope(from types.NodeID) *p2p.Envelope {
	return &p2p.Envelope{
		From:      from,
		ChannelID: p2p.ConsensusStateChannel,
		Message:   &tmcons.VoteSetMaj23{Height: 1, Round: 0, Type: tmproto.PrevoteType},
	}
}

func voteSetBitsEnvelope(from types.NodeID) *p2p.Envelope {
	return &p2p.Envelope{
		From:      from,
		ChannelID: p2p.VoteSetBitsChannel,
		Message:   &tmcons.VoteSetBits{Height: 1, Round: 0, Type: tmproto.PrevoteType},
	}
}

// The State and VoteSetBits channels carry no signature verification, so the
// verification budget does not bound them at all. One peer must still not be
// able to occupy the channel goroutine — nor, with VoteSetMaj23, make this node
// build and send a bit array per message it receives.
func TestStateChannelMessagesAreBoundedPerPeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A frozen clock refills nothing, so what gets through is the bucket the
	// peer started with and no more.
	r := newStateRateLimitedReactor(ctx, clockwork.NewFakeClock())

	admitted := 0
	for i := 0; i < 100*peerStateRateBurst; i++ {
		if r.allowStateChannelMessage(ctx, hasVoteEnvelope("attacker")) {
			admitted++
		}
	}
	assert.LessOrEqual(t, admitted, peerStateRateBurst,
		"one peer must not be able to occupy the state channel without bound")
	assert.Positive(t, admitted, "the limit must not shut an honest peer out entirely")

	// The same ceiling covers the VoteSetBits channel, which carries the
	// 10000-bit arrays this node's own answers are made of.
	assert.False(t, r.allowStateChannelMessage(ctx, voteSetBitsEnvelope("attacker")),
		"both unbudgeted channels draw on the sender's one allowance")
}

// A VoteSetMaj23 is a request for work: it makes this node build a bit array
// covering every validator and send it back. It cannot cost the same as a
// message that only updates a bit of peer state.
func TestVoteSetMaj23CostsMoreThanPlainStateMessages(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	count := func(env func(types.NodeID) *p2p.Envelope) int {
		r := newStateRateLimitedReactor(ctx, clockwork.NewFakeClock())
		admitted := 0
		for r.allowStateChannelMessage(ctx, env("peer")) {
			admitted++
		}
		return admitted
	}

	assert.Less(t, count(maj23Envelope), count(hasVoteEnvelope)/4,
		"a request that costs us a bit array must buy far fewer turns than a state update")
}

// The per-peer ceiling says nothing about what the peers can do between them,
// and identities are free. The surplus ceiling is what bounds the claims asked
// for beyond what the senders' own shares cover, so that a sender's private
// allowance is not the whole story.
//
// What the node answers altogether is that surplus plus one share per sender,
// and the shares are sized so the two halves add up to the node's capacity at
// the connection ceiling. Measured here with far more identities than any such
// ceiling permits, to show the surplus binds long before the sum of the private
// allowances does.
func TestVoteSetMaj23IsBoundedInAggregate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := newStateRateLimitedReactor(ctx, clockwork.NewFakeClock())

	// Far more identities than any connection ceiling permits, each with a full
	// private bucket: without a ceiling across peers they would all be served.
	const peers = 500
	admitted := 0
	for p := 0; p < peers; p++ {
		peer := types.NodeID(fmt.Sprintf("fresh-peer-%d", p))
		for r.allowStateChannelMessage(ctx, maj23Envelope(peer)) {
			admitted++
		}
	}

	assert.LessOrEqual(t, admitted, peers*maj23PeerShareBurst+maj23SurplusBurst,
		"fresh identities must not be able to buy more than one share each plus the shared surplus")
	assert.Less(t, admitted, peers*(peerStateRateBurst/maj23TokenCost),
		"the surplus ceiling must bind before the sum of the private ones")
}

// A node-wide ceiling with nothing reserved is first come, first served, and
// identities are what an attacker has most of: the slots it holds can fill the
// ceiling from inside their own private allowances and leave the validators
// this node has to reconcile votes with refused. That is the flaw the per-peer
// scheduling lanes exist to fix, one channel further out — and it lands on the
// message that recovers a vote lost to any of the other ceilings, so the
// reconciliation loop stops working exactly under the load it is for.
//
// Every peer must therefore be answered up to its own share whatever the rest
// are doing.
func TestHonestMajorityClaimsSurviveAnIdentityFlood(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Frozen: nothing refills, so the flood's effect on the honest peers is
	// what the ceilings decide and not what the clock hands back.
	r := newStateRateLimitedReactor(ctx, clockwork.NewFakeClock())

	// Most of the connection slots, each asking as fast as its own allowance
	// permits.
	const attackers = 56
	for a := 0; a < attackers; a++ {
		for r.allowStateChannelMessage(ctx, maj23Envelope(types.NodeID(fmt.Sprintf("attacker-%d", a)))) {
		}
	}

	// The validators in the remaining slots then ask for exactly what an honest
	// gossip loop offers in one pass.
	const honest = 12
	answered := 0
	for h := 0; h < honest; h++ {
		peer := types.NodeID(fmt.Sprintf("honest-%d", h))
		for i := 0; i < maj23ClaimsPerTick; i++ {
			if r.allowStateChannelMessage(ctx, maj23Envelope(peer)) {
				answered++
			}
		}
	}

	assert.Equal(t, honest*maj23ClaimsPerTick, answered,
		"a peer asking no more than its gossip loop offers must be answered whatever the other slots are doing")
}

// An honest peer must not spend its own allowance on a claim the node as a
// whole then refuses: its retries would be throttled hardest exactly when the
// channel is congested. The evidence channel's admission gate takes the same
// order, and both are correct only because a single goroutine stands between the
// look and the charge — which for majority claims is why they are only counted
// against the surplus when they arrive on the one channel that answers them.
func TestMajorityClaimRefusedNodeWideDoesNotChargeTheSender(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := newStateRateLimitedReactor(ctx, clockwork.NewFakeClock())

	// Drain the node-wide surplus with peers that are not the ones under test.
	drained := false
	for a := 0; a < 1000 && !drained; a++ {
		peer := types.NodeID(fmt.Sprintf("attacker-%d", a))
		spent := 0
		for r.allowStateChannelMessage(ctx, maj23Envelope(peer)) {
			spent++
		}
		// A peer that got no more than its own share means the surplus is gone.
		drained = spent <= maj23PeerShareBurst
	}
	require.True(t, drained, "the node-wide surplus was never exhausted, so the test proves nothing")

	// Two peers in the same position: both have spent their own share, so the
	// next claim either makes reaches the surplus.
	spendShare := func(peer types.NodeID) {
		for i := 0; i < maj23PeerShareBurst; i++ {
			require.True(t, r.allowStateChannelMessage(ctx, maj23Envelope(peer)))
		}
	}
	spendShare("charged")
	spendShare("control")

	require.False(t, r.allowStateChannelMessage(ctx, maj23Envelope("charged")),
		"the surplus is spent, so the claim must be refused")

	// What each peer has left of its own allowance for the channel.
	remaining := func(peer types.NodeID) int {
		left := 0
		for r.allowStateChannelMessage(ctx, hasVoteEnvelope(peer)) {
			left++
		}
		return left
	}
	assert.Equal(t, remaining("control"), remaining("charged"),
		"a claim the node refuses must not cost the sender its own allowance")
}

// The cheap state updates deliberately have no node-wide ceiling. They are sent
// once, on a state change, and never repeated, so a shared bucket a handful of
// identities could fill from inside their own legal allowances would drop
// honest round-step and vote announcements indiscriminately — leaving our
// picture of those peers stale and the gossip we send them wrong. Per-peer is
// where they are bounded.
func TestPlainStateMessagesAreNotSubjectToTheAggregateCeiling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := newStateRateLimitedReactor(ctx, clockwork.NewFakeClock())

	// Spend the whole node-wide majority-claim ceiling first.
	for r.allowStateChannelMessage(ctx, maj23Envelope("attacker")) {
	}

	assert.True(t, r.allowStateChannelMessage(ctx, hasVoteEnvelope("honest")),
		"a peer's own state updates must not be refused because of what others asked for")
}

// Answering a VoteSetMaj23 costs a bit array over every validator. A peer's
// gossip loop repeats the same claim on every tick, so answering each copy is
// work this node does over and over for a question it has already answered —
// while the answer, being derived only from our own vote set, has not changed.
func TestRepeatedVoteSetMaj23IsAnsweredOnlyWhenTheAnswerChanges(t *testing.T) {
	// A frozen clock keeps this about what the answer says, not how long ago it
	// was said; the aging of an answer is covered on its own.
	ps := NewPeerState(log.NewNopLogger(), "peer", WithPeerStateClock(clockwork.NewFakeClock()))
	blockID := types.BlockID{Hash: []byte("block")}

	answer := bits.NewBitArray(4)
	answer.SetIndex(0, true)

	require.True(t, ps.ShouldAnswerVoteSetMaj23(1, 0, tmproto.PrevoteType, blockID, answer),
		"the first claim must be answered")

	// Until the answer has gone out, the peer must keep being answered: an
	// answer this node failed to deliver leaves it waiting for votes it was
	// never told about.
	assert.True(t, ps.ShouldAnswerVoteSetMaj23(1, 0, tmproto.PrevoteType, blockID, answer),
		"only a delivered answer may suppress the next ask")

	ps.RecordVoteSetMaj23Answer(1, 0, tmproto.PrevoteType, blockID, answer)
	assert.False(t, ps.ShouldAnswerVoteSetMaj23(1, 0, tmproto.PrevoteType, blockID, answer),
		"repeating a claim we have already answered must not cost another answer")

	// Once our own vote set has moved on, the peer is told.
	answer.SetIndex(1, true)
	assert.True(t, ps.ShouldAnswerVoteSetMaj23(1, 0, tmproto.PrevoteType, blockID, answer),
		"a peer must learn about votes we have gained since we last answered it")

	// A different claim is a different question.
	ps.RecordVoteSetMaj23Answer(1, 0, tmproto.PrevoteType, blockID, answer)
	assert.True(t,
		ps.ShouldAnswerVoteSetMaj23(1, 1, tmproto.PrevoteType, blockID, answer),
		"a claim about another round must be answered on its own merits")

	// An answer carrying no votes at all is still an answer.
	ps.RecordVoteSetMaj23Answer(2, 0, tmproto.PrevoteType, blockID, nil)
	assert.False(t, ps.ShouldAnswerVoteSetMaj23(2, 0, tmproto.PrevoteType, blockID, nil),
		"a nil bit array must be handled like any other answer")
}

// The response is sent on the channel goroutine that serves every peer's state
// messages, so a router that cannot take it right now must not park that
// goroutine: one peer's slow link would stall the state channel for all of
// them. Nothing here is the sender's fault, so giving up is silent.
func TestVoteSetBitsResponseSendIsBounded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := &Reactor{logger: log.NewNopLogger(), clock: clockwork.NewRealClock()}

	start := time.Now()
	// A channel that never accepts anything stands in for a router that cannot
	// keep up.
	sent := r.sendVoteSetBits(ctx, blockedChannel{}, "peer", &tmcons.VoteSetBits{})
	elapsed := time.Since(start)

	assert.False(t, sent, "an undelivered answer must not be recorded as one")
	assert.Less(t, elapsed, 5*voteSetResponseTimeout,
		"the send must give up on its own rather than wait out the caller's context")
	require.NoError(t, ctx.Err(), "giving up must not have canceled the caller's context")
}

// blockedChannel is a p2p.Channel whose sends never complete.
type blockedChannel struct {
	p2p.Channel
}

func (blockedChannel) Send(ctx context.Context, _ p2p.Envelope) error {
	<-ctx.Done()
	return ctx.Err()
}

// The shares and the surplus are two halves of one budget. If they were tuned
// independently, the node would quietly commit to answering more than it was
// sized for — the shares alone are what every connected sender is guaranteed,
// and nothing at admission time can refuse them.
func TestMajorityClaimSharesAndSurplusAddUpToTheNodeCeiling(t *testing.T) {
	reserved := maj23PeerShareRate * maj23AssumedSlots
	assert.InDelta(t, float64(maj23NodeRateLimit), reserved+maj23SurplusRateLimit, 1,
		"every slot's share plus the surplus must be what the node answers altogether")
	assert.GreaterOrEqual(t, maj23PeerShareRate,
		float64(maj23ClaimsPerTick)/cfg.DefaultConsensusConfig().PeerQueryMaj23SleepDuration.Seconds(),
		"a share below honest demand would send an honest peer to the contended surplus every tick")
	assert.Positive(t, maj23SurplusBurst,
		"a peer asking beyond its share must have somewhere to go")

	// The share is what every connected sender is guaranteed, so how many senders
	// there can be is part of the sum. The p2p layer settles that and this one
	// cannot see it, so the assumption is pinned against the defaults it is taken
	// from.
	p2pDefaults := cfg.DefaultP2PConfig()
	assert.Equal(t, maj23AssumedSlots, int(p2pDefaults.MaxConnections)+defaultMaxConnectedUpgrade,
		"the slots the node's capacity is divided between must match the connection ceiling")
}

// defaultMaxConnectedUpgrade is the upgrade allowance the node applies on top of
// the configured connection ceiling. It is not a configuration value, so it is
// restated here rather than read.
const defaultMaxConnectedUpgrade = 4
