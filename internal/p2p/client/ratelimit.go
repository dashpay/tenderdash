package client

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

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

	logger log.Logger
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
func NewRateLimit(ctx context.Context, limit float64, drop bool, logger log.Logger) *RateLimit {
	h := &RateLimit{
		limiters: sync.Map{},
		limit:    limit,
		burst:    int(DefaultRecvBurstMultiplier * limit),
		drop:     drop,
		logger:   logger,
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

	limit.lastAccess.Store(time.Now().Unix())

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
// - nTokens: number of tokens to consume; use 1 if unsure
func (h *RateLimit) Limit(ctx context.Context, peerID types.NodeID, nTokens int) (allowed bool, err error) {
	if h.limit > 0 && peerID != "" {
		limiter := h.getLimiter(peerID)

		if h.drop {
			return limiter.AllowN(time.Now(), nTokens), nil
		}

		if err := limiter.WaitN(ctx, 1); err != nil {
			return false, fmt.Errorf("rate limit failed for peer %s: %w", peerID, err)
		}
	}
	return true, nil
}

// gcRoutine is a goroutine that removes unused limiters for peers every `PeerRateLimitLifetime` seconds.
func (h *RateLimit) gcRoutine(ctx context.Context) {
	ticker := time.NewTicker(PeerRateLimitLifetime * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.gc()
		}
	}
}

// GC removes old limiters.
func (h *RateLimit) gc() {
	now := time.Now().Unix()
	h.limiters.Range(func(key, value interface{}) bool {
		if value.(*limiter).lastAccess.Load() < now-60 {
			h.limiters.Delete(key)
		}
		return true
	})
}
