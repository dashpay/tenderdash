package client

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

// TestRateLimitBurstClampPermanentlyDropsHeavyMessage demonstrates that the
// cost-weighted rate limit proposed for the consensus channels
// (nTokens = 1 + len(vote.VoteExtensions)) silently and PERMANENTLY drops any
// single message whose weight exceeds the token bucket's burst capacity.
//
// NewRateLimit derives burst = DefaultRecvBurstMultiplier * limit = 10 * limit.
// golang.org/x/time/rate returns ok=false from AllowN whenever n > burst, no
// matter how long the caller waits, because the bucket can never accumulate
// more than `burst` tokens. With drop=true this is an unconditional, silent,
// unrecoverable drop -- not a delay.
//
// Consequence for consensus liveness: if the ABCI application ever returns more
// vote extensions per precommit than the configured burst allows, every honest
// validator's precommits are dropped forever and the chain halts. The operator
// sees no error; the message never reaches the WAL, the vote set, or the log.
func TestRateLimitBurstClampPermanentlyDropsHeavyMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const limit = 50.0 // hypothetical `peer-msg-rate-limit = 50`
	rl := NewRateLimit(ctx, limit, true /* drop */, log.NewNopLogger())

	require.Equal(t, int(DefaultRecvBurstMultiplier*limit), rl.burst,
		"burst is derived from the limit; the operator cannot size it independently")

	peer := types.NodeID("0102030405060708090a0b0c0d0e0f1011121314")

	// A precommit carrying 600 vote extensions weighs 1+600 = 601 tokens.
	// That is well under the configured 50/s * 12s of accumulated budget a
	// naive reading suggests, but it is above burst (500).
	const heavyMsgTokens = 601
	require.Greater(t, heavyMsgTokens, rl.burst)

	// Drain nothing: the bucket starts full at `burst` tokens.
	// Even on the very first call, with a completely idle limiter, the message
	// is rejected.
	allowed, err := rl.Limit(ctx, peer, heavyMsgTokens)
	require.NoError(t, err)
	assert.False(t, allowed,
		"a message heavier than burst is rejected even against a full bucket")

	// And it stays rejected forever -- this is not backpressure, it is a wall.
	// (Waiting cannot help: the bucket is capped at `burst`.)
	for i := 0; i < 100; i++ {
		allowed, err = rl.Limit(ctx, peer, heavyMsgTokens)
		require.NoError(t, err)
		require.False(t, allowed, "attempt %d: still rejected", i)
	}

	// Control: the same peer's *cheap* messages sail through, so the peer is
	// never observably "rate limited" -- only the heavy message class vanishes.
	allowed, err = rl.Limit(ctx, peer, 1)
	require.NoError(t, err)
	assert.True(t, allowed)
}

// TestRateLimitWaitPathChargesNTokens pins the cost weighting on the waiting
// path. A limiter that delays over-budget messages rather than dropping them
// must charge the caller's weight just as the dropping path does; charging a
// flat token instead would leave the message uncharged and uncapped, and the
// whole security argument for these limits is that the budget is denominated in
// the work a message forces, not in messages.
func TestRateLimitWaitPathChargesNTokens(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// limit=1000/s, burst=10000; a 5000-token message consumes half the burst.
	rl := NewRateLimit(ctx, 1000, false /* wait, do not drop */, log.NewNopLogger())
	peer := types.NodeID("0102030405060708090a0b0c0d0e0f1011121314")

	allowed, err := rl.Limit(ctx, peer, 5000)
	require.NoError(t, err)
	require.True(t, allowed)

	// The bounds allow for the handful of tokens the bucket refills while the
	// test runs; charging one token per message would leave ~9999.
	tokensLeft := rl.getLimiter(peer).Tokens()
	assert.Greater(t, tokensLeft, 4900.0, "got %f tokens left, expected ~5000", tokensLeft)
	assert.Less(t, tokensLeft, 5100.0, "got %f tokens left, expected ~5000", tokensLeft)
}
