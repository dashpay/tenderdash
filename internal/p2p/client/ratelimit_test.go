package client

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/conn"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

// TestRecvRateLimitHandler tests the rate limit middleware when receiving messages from peers.
// It tests that the rate limit is applied per peer.
//
// GIVEN 5 peers named 1..5 and rate limit of 2/s and burst 4,
// WHEN we send 1, 2, 3, 4 and 5 msgs per second respectively for 3 seconds,
// THEN:
// * peer 1 and 2 receive all messages,
// * other peers receive 2 messages per second plus 4 burst messages.
//
// Reuses testRateLimit from client_test.go
func TestRecvRateLimitHandler(t *testing.T) {
	// don't run this if we are in short mode
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	const (
		Limit           = 2.0
		Burst           = 4
		Peers           = 5
		TestTimeSeconds = 3
	)

	sent := make([]atomic.Uint32, Peers)

	fakeHandler := newMockConsumer(t)
	fakeHandler.On("Handle", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).
		Run(func(args mock.Arguments) {
			peerID := args.Get(2).(*p2p.Envelope).From
			peerNum, err := strconv.Atoi(string(peerID))
			require.NoError(t, err)
			sent[peerNum-1].Add(1)
		})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := log.NewTestingLogger(t)
	client := &Client{}

	mw := WithRecvRateLimitPerPeerHandler(ctx,
		Limit,
		func(*p2p.Envelope) uint { return 1 },
		false,
		logger,
	)(fakeHandler).(*recvRateLimitPerPeerHandler)

	mw.burst = Burst

	sendFn := func(peerID types.NodeID) error {
		envelope := p2p.Envelope{
			From:      peerID,
			ChannelID: testChannelID,
		}
		return mw.Handle(ctx, client, &envelope)
	}

	parallelSendWithLimit(t, ctx, sendFn, Peers, TestTimeSeconds)
	assertRateLimits(t, sent, Limit, Burst, TestTimeSeconds)
}

// TestSendRateLimit tests the rate limit for sending messages using p2p.client.
//
// Each peer should have his own, independent rate limit.
//
// GIVEN 5 peers named 1..5 and rate limit of 2/s and burst 4,
// WHEN we send 1, 2, 3, 4 and 5 msgs per second respectively for 3 seconds,
// THEN:
// * peer 1 and 2 receive all messages,
// * other peers receive 2 messages per second plus 4 burst messages.
func (suite *ChannelTestSuite) TestSendRateLimit() {
	if testing.Short() {
		suite.T().Skip("skipping test in short mode.")
	}

	const (
		Limit           = 2.0
		Burst           = 4
		Peers           = 5
		TestTimeSeconds = 3
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := suite.client

	limiter := NewRateLimit(ctx, Limit, false, suite.client.logger)
	limiter.burst = Burst
	suite.client.rateLimit = map[conn.ChannelID]*RateLimit{
		testChannelID: limiter,
	}

	sendFn := func(peerID types.NodeID) error {
		envelope := p2p.Envelope{
			To:        peerID,
			ChannelID: testChannelID,
		}
		return client.Send(ctx, envelope)

	}
	sent := make([]atomic.Uint32, Peers)

	suite.p2pChannel.On("Send", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			peerID := args.Get(1).(p2p.Envelope).To
			peerNum, err := strconv.Atoi(string(peerID))
			suite.NoError(err)
			sent[peerNum-1].Add(1)
		}).
		Return(nil)

	parallelSendWithLimit(suite.T(), ctx, sendFn, Peers, TestTimeSeconds)
	assertRateLimits(suite.T(), sent, Limit, Burst, TestTimeSeconds)
}

// parallelSendWithLimit sends messages to peers in parallel with a rate limit.
//
// The function sends messages to peers. Each peer gets its number, starting from 1.
// Rate limit is equal to the peer number, eg. peer 1 sends 1 msg/s, peeer 2 sends 2 msg/s etc.
func parallelSendWithLimit(t *testing.T, ctx context.Context, sendFn func(peerID types.NodeID) error,
	peers int, testTimeSeconds int) {
	t.Helper()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// all goroutines will wait for the start signal
	start := sync.RWMutex{}
	start.Lock()

	for peer := 1; peer <= peers; peer++ {
		peerID := types.NodeID(strconv.Itoa(peer))
		// peer number is the rate limit
		msgsPerSec := peer

		go func(peerID types.NodeID, rate int) {
			start.RLock()
			defer start.RUnlock()

			for s := 0; s < testTimeSeconds; s++ {
				until := time.NewTimer(time.Second)
				defer until.Stop()

				for i := 0; i < rate; i++ {
					select {
					case <-ctx.Done():
						return
					default:
					}

					if err := sendFn(peerID); !errors.Is(err, context.Canceled) {
						require.NoError(t, err)
					}
				}

				select {
				case <-until.C:
					// noop, we just sleep until the end of the second
				case <-ctx.Done():
					return
				}
			}

		}(peerID, msgsPerSec)
	}

	// start the test
	startTime := time.Now()
	start.Unlock()
	runtime.Gosched()
	time.Sleep(time.Duration(testTimeSeconds) * time.Second)
	cancel()
	// wait for all goroutines to finish, that is - drop RLocks
	start.Lock()
	defer start.Unlock()

	// The lower bound is guaranteed by the time.Sleep above; only check the upper bound.
	elapsed := time.Since(startTime)
	assert.LessOrEqual(t, elapsed.Seconds(), float64(testTimeSeconds)+2.0,
		"test should not run more than %d+2 seconds", testTimeSeconds)
}

// assertRateLimits checks if the rate limits were applied correctly.
// We assume that index of each item in `sent` is the peer number, as described in parallelSendWithLimit.
// We use a tolerance of ±1 to accommodate scheduling jitter in the rate limiter.
func assertRateLimits(t *testing.T, sent []atomic.Uint32, limit float64, burst int, seconds int) {
	t.Helper()
	for peer := 1; peer <= len(sent); peer++ {
		expected := int(limit)*seconds + burst
		if expected > peer*seconds {
			expected = peer * seconds
		}
		actual := int(sent[peer-1].Load())
		assert.GreaterOrEqual(t, actual, expected-1, "peer %d received too few messages (expected ~%d)", peer, expected)
		assert.LessOrEqual(t, actual, expected+1, "peer %d received too many messages (expected ~%d)", peer, expected)
	}
}

func TestNewRateLimitWithBurst_FloorsZeroBurst(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A positive limit with a computed burst of 0 (e.g. int(2*0.1)) would reject
	// every message; it must be floored to 1 so the channel is not silently
	// broken.
	rl := NewRateLimitWithBurst(ctx, 0.1, 0, true, log.NewNopLogger())
	require.Equal(t, 1, rl.burst)

	allowed, err := rl.Limit(ctx, "peer", 1)
	require.NoError(t, err)
	require.True(t, allowed, "a floored burst of 1 must allow at least one message")
}

func TestNewRateLimitWithBurst_ExplicitBurstHonored(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rl := NewRateLimitWithBurst(ctx, 100, 200, true, log.NewNopLogger())
	require.Equal(t, 200, rl.burst, "explicit burst must be used, not the 10x default")

	def := NewRateLimit(ctx, 100, true, log.NewNopLogger())
	require.Equal(t, int(DefaultRecvBurstMultiplier*100), def.burst)
}

// The limiter must meter time through an injectable clock, so that refill
// behaviour can be asserted exactly instead of being inferred from sleeps. A
// test that cannot advance time can only observe the bucket draining, never the
// rate at which it recovers — which is the property the limit actually promises.
func TestRateLimit_RefillsOnInjectedClock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clock := clockwork.NewFakeClock()
	const (
		limit = 10.0
		burst = 10
	)
	rl := NewRateLimitWithBurst(ctx, limit, burst, true, log.NewNopLogger(), WithRateLimitClock(clock))

	peer := types.NodeID("0102030405060708090a0b0c0d0e0f1011121314")

	// Drain the bucket. No time passes, so nothing refills.
	for i := 0; i < burst; i++ {
		allowed, err := rl.Limit(ctx, peer, 1)
		require.NoError(t, err)
		require.True(t, allowed, "message %d must fit in a full bucket", i)
	}
	allowed, err := rl.Limit(ctx, peer, 1)
	require.NoError(t, err)
	require.False(t, allowed, "an empty bucket must reject until time passes")

	// One second at limit=10/s refills exactly ten tokens, and no more: the
	// bucket is capped at burst.
	clock.Advance(time.Second)
	for i := 0; i < burst; i++ {
		allowed, err = rl.Limit(ctx, peer, 1)
		require.NoError(t, err)
		require.True(t, allowed, "refilled message %d must be allowed", i)
	}
	allowed, err = rl.Limit(ctx, peer, 1)
	require.NoError(t, err)
	assert.False(t, allowed, "refill must stop at the configured rate")
}

// newWaitingRateLimit returns a limiter that delays over-budget messages
// instead of dropping them, metered against clock. It returns once the garbage
// collector's ticker is registered with the clock, so a test can tell the
// limiter's own wait apart from it by the number of waiters.
func newWaitingRateLimit(ctx context.Context, t *testing.T, limit float64, burst int, clock *clockwork.FakeClock) *RateLimit {
	t.Helper()

	rl := NewRateLimitWithBurst(ctx, limit, burst, false, log.NewNopLogger(), WithRateLimitClock(clock))
	require.NoError(t, clock.BlockUntilContext(ctx, 1), "garbage collector did not start")

	return rl
}

// The waiting path must meter against the injected clock. A wait driven by the
// wall clock cannot be released by a test that controls time, so how long a
// message is delayed — the whole promise of waiting rather than dropping —
// could only be inferred from sleeps.
func TestRateLimit_WaitPathRefillsOnInjectedClock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const (
		limit = 10.0
		burst = 10
	)
	clock := clockwork.NewFakeClock()
	rl := newWaitingRateLimit(ctx, t, limit, burst, clock)
	peer := types.NodeID("0102030405060708090a0b0c0d0e0f1011121314")

	// Empty the bucket. A full bucket covers this at once, so nothing waits.
	allowed, err := rl.Limit(ctx, peer, burst)
	require.NoError(t, err)
	require.True(t, allowed)

	admitted := make(chan error, 1)
	go func() {
		_, err := rl.Limit(ctx, peer, 5)
		admitted <- err
	}()

	require.NoError(t, clock.BlockUntilContext(ctx, 2), "the limiter is not waiting on the injected clock")
	select {
	case <-admitted:
		t.Fatal("an empty bucket admitted a message without any time passing")
	default:
	}

	// At 10 tokens/s, five tokens take half a second and not a moment less.
	clock.Advance(500 * time.Millisecond)
	select {
	case err := <-admitted:
		require.NoError(t, err, "the message must be admitted once the bucket has refilled")
	case <-time.After(5 * time.Second):
		t.Fatal("the message was not admitted after the bucket refilled")
	}
}

// A waiting caller must give up when its context is cancelled — a wait that
// ignored cancellation would hold the caller through shutdown — and the tokens
// it never spent must go back to the bucket.
func TestRateLimit_WaitPathHonoursContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const (
		limit = 10.0
		burst = 10
	)
	clock := clockwork.NewFakeClock()
	rl := newWaitingRateLimit(ctx, t, limit, burst, clock)
	peer := types.NodeID("0102030405060708090a0b0c0d0e0f1011121314")

	allowed, err := rl.Limit(ctx, peer, burst)
	require.NoError(t, err)
	require.True(t, allowed)

	waitCtx, cancelWait := context.WithCancel(ctx)
	type result struct {
		allowed bool
		err     error
	}
	results := make(chan result, 1)
	go func() {
		allowed, err := rl.Limit(waitCtx, peer, 5)
		results <- result{allowed: allowed, err: err}
	}()

	require.NoError(t, clock.BlockUntilContext(ctx, 2), "the limiter is not waiting on the injected clock")
	cancelWait()

	select {
	case res := <-results:
		require.Error(t, res.err, "a cancelled wait must report failure rather than admit the message")
		assert.False(t, res.allowed)
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled wait did not return")
	}

	// The abandoned wait must not have spent the tokens it was queued for:
	// half a second of refill still buys a five-token message outright.
	clock.Advance(500 * time.Millisecond)
	admitted := make(chan error, 1)
	go func() {
		_, err := rl.Limit(ctx, peer, 5)
		admitted <- err
	}()
	select {
	case err := <-admitted:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled wait consumed tokens it never used")
	}
}

// Without an explicit clock the limiter keeps metering wall-clock time, so
// production behaviour is unchanged by the seam.
func TestRateLimit_DefaultsToRealClock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rl := NewRateLimitWithBurst(ctx, 10, 10, true, log.NewNopLogger())
	require.IsType(t, clockwork.NewRealClock(), rl.clock)
}

func TestNewRateLimitWithBurst_DisabledLimitKeepsZeroBurst(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rl := NewRateLimitWithBurst(ctx, 0, 0, true, log.NewNopLogger())
	require.Equal(t, 0, rl.burst)
}

// A caller whose own deadline is shorter than the delay the bucket asks for
// gains nothing by waiting it out: it will be cancelled before the tokens
// arrive. Deciding that up front is what keeps the caller — and, on a shared
// channel goroutine, every message behind it — from being parked for a
// deadline's worth of time to reach the same refusal, and it is what lets the
// caller tell "the bucket cannot serve you in time" apart from "you were
// cancelled".
func TestRateLimit_WaitFailsFastPastTheCallerDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const (
		limit    = 1.0
		burst    = 10
		deadline = 300 * time.Millisecond
	)
	// A real clock: the caller's deadline is wall-clock time, so the delay it
	// is compared against has to be measured on the same clock.
	rl := NewRateLimitWithBurst(ctx, limit, burst, false, log.NewNopLogger())
	peer := types.NodeID("0102030405060708090a0b0c0d0e0f1011121314")

	// Empty the bucket, so the next full-burst request needs ten seconds.
	allowed, err := rl.Limit(ctx, peer, burst)
	require.NoError(t, err)
	require.True(t, allowed)

	shortCtx, cancelShort := context.WithTimeout(ctx, deadline)
	defer cancelShort()

	start := time.Now()
	allowed, err = rl.Limit(shortCtx, peer, burst)
	elapsed := time.Since(start)

	assert.False(t, allowed)
	require.Error(t, err)
	assert.NotErrorIs(t, err, context.DeadlineExceeded,
		"a delay the caller could never wait out is the bucket refusing, not the caller giving up")
	assert.Less(t, elapsed, deadline,
		"the refusal must be decided up front, not by sitting out the caller's deadline")

	// Giving up must hand the tokens back: a peer is not charged for a wait
	// that was refused before it began.
	require.NoError(t, ctx.Err())
	allowed, err = rl.Limit(ctx, peer, 1)
	require.NoError(t, err)
	assert.True(t, allowed, "a fast-failed request must not have consumed the bucket")
}
