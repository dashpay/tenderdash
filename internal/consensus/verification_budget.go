package consensus

import (
	"context"
	"time"

	"github.com/jonboulle/clockwork"
	"golang.org/x/time/rate"

	"github.com/dashpay/tenderdash/config"
)

const (
	// verificationBurstMargin sizes the global token bucket as a multiple of the
	// most expensive message a peer can send. A bucket exactly that size would
	// admit such a message only while it is completely full — that is, only on an
	// idle node — so the margin is what keeps the worst case admissible under
	// load.
	verificationBurstMargin = 2

	// verificationBudgetBurst is the size of the node-wide bucket of signature
	// verifications that peer messages are charged against.
	verificationBudgetBurst = verificationBurstMargin * maxPeerMessageCost

	// verificationWaitMargin bounds how long a message may wait for budget, as a
	// multiple of the time the bucket needs to refill the most expensive message
	// from empty. Nothing needs longer, so a wait beyond it means the budget is
	// not merely momentarily short, and the message is better dropped than left
	// holding the consensus goroutine.
	verificationWaitMargin = 2
)

// The bucket must be able to hold strictly more than the most expensive single
// message. A change to either side that breaks that relation fails to compile,
// which is the point: MaxVoteExtensions invites being raised, and a burst left
// behind at the old value would make the most expensive message permanently
// inadmissible.
const _ uint = verificationBudgetBurst - maxPeerMessageCost - 1

// The minimum configurable rate is what makes the bounded wait usable: it
// guarantees the bucket refills the most expensive message within a second, so
// waiting for one costs milliseconds rather than minutes.
const _ uint = config.MinVerificationRateLimit - maxPeerMessageCost

// budgetWaiter is the part of a verification budget that can defer a message
// until its whole cost is affordable. It is optional: a budget that cannot wait
// simply never defers one.
type budgetWaiter interface {
	waitFor(ctx context.Context, cost int) bool
}

// The budget this node ships with must be able to wait. Callers ask for this
// through a type assertion and quietly stop deferring messages when it fails,
// which would give up whole-message atomicity without a single test turning
// red; a budget that loses the method fails to compile instead.
var _ budgetWaiter = (*rateVerificationBudget)(nil)

// budgetSaturationReporter is the part of a verification budget that can report
// how full it is, for the saturation gauge. It is optional: a budget that
// cannot report is simply never sampled.
type budgetSaturationReporter interface {
	saturation() float64
}

var _ budgetSaturationReporter = (*rateVerificationBudget)(nil)

type rateVerificationBudget struct {
	limiter *rate.Limiter
	// clock is the time source the bucket is metered against; the wall clock
	// unless overridden.
	clock clockwork.Clock
	// maxWait bounds how long waitFor may hold the consensus goroutine.
	maxWait time.Duration
}

// verificationBudgetOptionFunc overrides a default parameter of a
// rateVerificationBudget.
type verificationBudgetOptionFunc func(*rateVerificationBudget)

// withVerificationBudgetClock sets the time source the budget is metered
// against. The default is the wall clock; a test injects a fake clock to
// advance time explicitly, which is the only way to assert how long a message
// waits rather than merely that it eventually runs.
func withVerificationBudgetClock(clock clockwork.Clock) verificationBudgetOptionFunc {
	return func(b *rateVerificationBudget) {
		b.clock = clock
	}
}

func newVerificationBudget(limit float64, opts ...verificationBudgetOptionFunc) *rateVerificationBudget {
	budget := &rateVerificationBudget{clock: clockwork.NewRealClock()}
	if limit > 0 {
		budget.limiter = rate.NewLimiter(rate.Limit(limit), verificationBudgetBurst)
		budget.maxWait = time.Duration(float64(verificationWaitMargin*maxPeerMessageCost) / limit * float64(time.Second))
	}
	for _, opt := range opts {
		opt(budget)
	}
	return budget
}

func (b *rateVerificationBudget) Allow(cost int) bool {
	if b == nil || b.limiter == nil {
		return true
	}
	return b.allowN(b.clock.Now(), cost)
}

// saturation reports how full the token bucket is, from 1.0 when it is
// untouched to 0.0 when it holds nothing. A budget that does not meter — rate
// limiting disabled — reports 1.0, since nothing is ever short.
//
// It reads the limiter's own tokens, which takes the limiter's mutex; callers
// sample it next to a budget check that already takes it, so it adds no lock of
// its own.
func (b *rateVerificationBudget) saturation() float64 {
	if b == nil || b.limiter == nil {
		return 1
	}
	return b.limiter.TokensAt(b.clock.Now()) / float64(b.limiter.Burst())
}

func (b *rateVerificationBudget) allowN(now time.Time, cost int) bool {
	if b == nil || b.limiter == nil {
		return true
	}
	return b.limiter.AllowN(now, cost)
}

// waitFor blocks until the bucket holds enough tokens to cover cost — the whole
// verification work one message can force — and reports whether the caller may
// proceed.
//
// Waiting rather than refusing is what keeps the budget cost-neutral. Refusing a
// message costs microseconds, so a saturating flood of cheap messages would hold
// the bucket below the cost of one expensive message indefinitely, and that
// message would never be admitted however honest its sender.
//
// Waiting takes no tokens; it only reports that enough are there. That is
// enough to make the staged charges that follow safe, but only because at most
// one peer message is ever between this call and its charges: the peer
// scheduler will not look at the budget again until the consensus goroutine has
// finished with the message it was handed (see peerLanes.awaitSettled), and
// nothing else charges. A second charging goroutine, or a buffered handoff to
// the consensus goroutine, would let two messages read the same tokens and
// spend them twice.
//
// The wait is bounded and honors ctx. An unbounded wait would stall consensus
// behind an over-subscribed budget, and a wait that ignored cancellation would
// keep the consensus goroutine from returning at shutdown. Both give up
// silently: local overload is not the sender's fault.
func (b *rateVerificationBudget) waitFor(ctx context.Context, cost int) bool {
	if b == nil || b.limiter == nil || cost <= 0 {
		return true
	}
	if cost > b.limiter.Burst() {
		// The bucket can never hold this much, so waiting could only stall.
		return false
	}
	deadline := b.clock.Now().Add(b.maxWait)
	for {
		now := b.clock.Now()
		missing := float64(cost) - b.limiter.TokensAt(now)
		if missing <= 0 {
			return true
		}
		// The extra nanosecond keeps rounding down from leaving the bucket a
		// fraction of a token short and forcing another pass through the loop.
		wait := time.Duration(missing/float64(b.limiter.Limit())*float64(time.Second)) + time.Nanosecond
		if now.Add(wait).After(deadline) {
			return false
		}
		timer := b.clock.NewTimer(wait)
		select {
		case <-timer.Chan():
		case <-ctx.Done():
			timer.Stop()
			return false
		}
	}
}
