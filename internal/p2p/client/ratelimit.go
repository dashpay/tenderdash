package client

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

type TokenNumberFunc func(*p2p.Envelope) uint

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

// NewRateLimit creates a new rate limiter.
//
// # Arguments
//
// * `limit` - rate limit per peer per second; 0 means no limit
// * `drop` - silently drop the message if the rate limit is exceeded; otherwise we will wait until the message is allowed
// * `logger` - logger
func NewRateLimit(limit float64, drop bool, logger log.Logger) *RateLimit {
	return &RateLimit{
		limiters: sync.Map{},
		limit:    limit,
		burst:    int(DefaultRecvBurstMultiplier * limit),
		drop:     drop,
		logger:   logger,
	}
}

func (h *RateLimit) getLimiter(peerID types.NodeID) *rate.Limiter {
	var limiter *rate.Limiter
	if l, ok := h.limiters.Load(peerID); ok {
		limiter = l.(*rate.Limiter)
	} else {
		limiter = rate.NewLimiter(rate.Limit(h.limit), h.burst)
		// we have a slight race condition here, possibly overwriting the limiter, but it's not a big deal
		// as the worst case scenario is that we allow one or two more messages than we should
		h.limiters.Store(peerID, limiter)
	}

	return limiter
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
