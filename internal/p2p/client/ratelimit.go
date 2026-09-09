package client

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jonboulle/clockwork"
	"golang.org/x/time/rate"

	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

const PeerRateLimitLifetime = 60 // number of seconds to keep the rate limiter for a peer

// RateLimit is a rate limiter for p2p messages.
// It is used to limit the rate of incoming messages from a peer.
// Each peer has its own independent limit.
//
// Use NewRateLimit to create a new rate limiter.
// Use [Limit()] to wait for the rate limit to allow the message to be sent.
type RateLimit struct {
	// limit is the rate limit per peer per second; 0 means no limit
	limit float64
	// burst is the initial number of tokens; see rate module for more details
	burst int
	// map of peerID to rate.Limiter
	limiters sync.Map
	// drop is a flag to silently drop the message if the rate limit is exceeded; otherwise we will wait
	drop bool
	// clock is the time source the limiter meters against; the wall clock unless overridden
	clock clockwork.Clock

	logger log.Logger
}

// RateLimitOptionFunc overrides a default parameter of a RateLimit.
type RateLimitOptionFunc func(*RateLimit)

// WithRateLimitClock sets the time source the limiter meters against. The
// default is the wall clock; a test injects a fake clock to advance time
// explicitly, which is the only way to assert how fast the bucket refills
// rather than merely that it drains.
func WithRateLimitClock(clock clockwork.Clock) RateLimitOptionFunc {
	return func(h *RateLimit) {
		h.clock = clock
	}
}

type limiter struct {
	*rate.Limiter
	// lastAccess is the last time the limiter was accessed, as Unix time (seconds)
	lastAccess atomic.Int64
}

// NewRateLimit creates a new rate limiter.
//
// # Arguments
//
// * `ctx` - context; used to gracefully shutdown the garbage collection routine
// * `limit` - rate limit per peer per second; 0 means no limit
// * `drop` - silently drop the message if the rate limit is exceeded; otherwise we will wait until the message is allowed
// * `logger` - logger
// * `opts` - optional overrides of the limiter defaults
func NewRateLimit(ctx context.Context, limit float64, drop bool, logger log.Logger, opts ...RateLimitOptionFunc) *RateLimit {
	return NewRateLimitWithBurst(ctx, limit, int(DefaultRecvBurstMultiplier*limit), drop, logger, opts...)
}

// NewRateLimitWithBurst is like NewRateLimit but takes an explicit burst (the
// number of tokens the bucket may accumulate). Use this when the default
// 10x-rate burst is too permissive — e.g. a channel whose messages are
// individually expensive, where a large burst lets a peer front-load a lot of
// work in one instant.
//
// If limit > 0, burst is floored at 1: a positive limit with a zero burst would
// reject every message (AllowN can never satisfy n=1 against a 0-token bucket),
// silently breaking the channel.
func NewRateLimitWithBurst(
	ctx context.Context,
	limit float64,
	burst int,
	drop bool,
	logger log.Logger,
	opts ...RateLimitOptionFunc,
) *RateLimit {
	if limit > 0 && burst < 1 {
		burst = 1
	}
	h := &RateLimit{
		limiters: sync.Map{},
		limit:    limit,
		burst:    burst,
		drop:     drop,
		clock:    clockwork.NewRealClock(),
		logger:   logger,
	}
	for _, opt := range opts {
		opt(h)
	}

	// start the garbage collection routine
	go h.gcRoutine(ctx)

	return h
}

func (h *RateLimit) getLimiter(peerID types.NodeID) *limiter {
	var limit *limiter
	if l, ok := h.limiters.Load(peerID); ok {
		limit = l.(*limiter)
	} else {
		limit = &limiter{Limiter: rate.NewLimiter(rate.Limit(h.limit), h.burst)}
		// we have a slight race condition here, possibly overwriting the limiter, but it's not a big deal
		// as the worst case scenario is that we allow one or two more messages than we should
		h.limiters.Store(peerID, limit)
	}

	limit.lastAccess.Store(h.clock.Now().Unix())

	return limit
}

// Limit waits for the rate limit to allow the message to be sent.
// It returns true if the message is allowed, false otherwise.
//
// If peerID is empty, messages is always allowed.
//
// Returns true when the message is allowed, false if it should be dropped.
//
// Arguments:
// - ctx: context
// - peerID: peer ID; if empty, the message is always allowed
// - nTokens: number of tokens to consume; use 1 if unsure. Both the dropping
// and the waiting variant charge the full weight, so a caller that prices
// messages by the work they force gets that pricing either way.
func (h *RateLimit) Limit(ctx context.Context, peerID types.NodeID, nTokens int) (allowed bool, err error) {
	if h.limit > 0 && peerID != "" {
		limiter := h.getLimiter(peerID)

		if h.drop {
			return limiter.AllowN(h.clock.Now(), nTokens), nil
		}

		if err := h.waitN(ctx, limiter, nTokens); err != nil {
			return false, fmt.Errorf("rate limit failed for peer %s: %w", peerID, err)
		}
	}
	return true, nil
}

// waitN blocks until the bucket can pay nTokens, then charges them.
//
// It is rate.Limiter.WaitN written against the injected clock: WaitN reads the
// wall clock internally, so a caller metering against any other time source
// would be released by time the rest of the limiter never sees. The reservation
// is what makes waiting safe under concurrency — the tokens are taken up front,
// so two callers waiting at once queue behind each other instead of both
// concluding the bucket is about to be full enough.
//
// A caller that gives up hands the tokens back, so an abandoned wait costs the
// peer nothing beyond the time already elapsed. A delay the caller's own
// deadline could never cover is refused up front for the same reason: sitting
// it out reaches the identical refusal, having held the tokens — and the
// caller's goroutine — for the whole deadline.
func (h *RateLimit) waitN(ctx context.Context, l *limiter, nTokens int) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	reservation := l.ReserveN(h.clock.Now(), nTokens)
	if !reservation.OK() {
		// The bucket can never hold this much, so waiting could only stall
		// forever.
		return fmt.Errorf("message of %d tokens exceeds the burst of %d", nTokens, l.Burst())
	}

	now := h.clock.Now()
	delay := reservation.DelayFrom(now)
	if delay <= 0 {
		return nil
	}
	if deadline, ok := ctx.Deadline(); ok && now.Add(delay).After(deadline) {
		// The reservation has not come due yet, so canceling it returns every
		// token: the peer is not charged for a wait that never happened.
		reservation.CancelAt(now)
		return fmt.Errorf("%d tokens need %s, past the caller's deadline", nTokens, delay)
	}

	timer := h.clock.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.Chan():
		return nil
	case <-ctx.Done():
		reservation.CancelAt(h.clock.Now())
		return ctx.Err()
	}
}

// gcRoutine is a goroutine that removes unused limiters for peers every `PeerRateLimitLifetime` seconds.
func (h *RateLimit) gcRoutine(ctx context.Context) {
	ticker := h.clock.NewTicker(PeerRateLimitLifetime * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.Chan():
			h.gc()
		}
	}
}

// GC removes old limiters.
func (h *RateLimit) gc() {
	now := h.clock.Now().Unix()
	h.limiters.Range(func(key, value interface{}) bool {
		if value.(*limiter).lastAccess.Load() < now-60 {
			h.limiters.Delete(key)
		}
		return true
	})
}
