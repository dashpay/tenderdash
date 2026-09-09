package evidence

import (
	"context"

	"golang.org/x/time/rate"

	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

// Evidence admission is denominated in signature verifications (pairings) — the
// same unit the consensus channels charge in, so the two ceilings can be
// compared. Verifying one DuplicateVoteEvidence forces two.
const evidenceVerifyCost = 2

const (
	// peerEvidenceRate is the sustained verification work a single peer may
	// cause, in pairings per second: one novel piece of evidence every two
	// seconds. Evidence is rare — a peer has something genuinely new for us
	// only when a validator actually equivocates, and copies of what we already
	// hold are refused for free — so a low sustained rate costs an honest peer
	// nothing.
	peerEvidenceRate = 1.0

	// peerEvidenceBurst is what serves honest catch-up: eight novel items at
	// once, with the rest of a peer's pool arriving over the following seconds
	// as it re-sends. It is deliberately small in absolute terms because a fresh
	// node ID gets a fresh full bucket, so the burst is exactly what an attacker
	// harvests per identity it rotates through.
	peerEvidenceBurst = 8 * evidenceVerifyCost

	// nodeEvidenceRate bounds the whole channel, so the cost of the evidence
	// reactor does not scale with the number of peers connected to us. It is
	// set above assumedMaxPeers * peerEvidenceRate with margin, so peers inside
	// their own allowance never compete for it: with a fixed peer set the
	// node-wide bucket refills faster than the sum of what every peer may
	// spend, returns to full, and stops being the binding constraint. Without
	// that margin the bucket would hover at zero and admission would become a
	// race an attacker usually wins.
	nodeEvidenceRate = 160.0

	// nodeEvidenceBurst absorbs eighty novel items arriving at once.
	nodeEvidenceBurst = 80 * evidenceVerifyCost

	// assumedMaxPeers is the connection ceiling the node-wide rate is sized
	// against: MaxConnected plus MaxConnectedUpgrade at their defaults.
	assumedMaxPeers = 68
)

// A bucket smaller than one message's cost rejects that message forever, no
// matter how long it waits, which would delete the whole evidence channel
// rather than throttle it. These fail the build if a limit is ever tuned below
// what a single message costs.
// The relation between the node-wide rate and the per-peer rate matters just as
// much, but it is a ratio of rates rather than of whole tokens, so it is
// asserted by test instead.
const (
	_ = uint(peerEvidenceBurst - evidenceVerifyCost)
	_ = uint(nodeEvidenceBurst - evidenceVerifyCost)
)

// admitFree reports whether an inbound piece of evidence is worth spending
// anything on. It runs before the evidence is hashed for the pool's duplicate
// check, so a flood of re-encoded copies of evidence we already hold is
// recognized by a map lookup rather than by digesting a message that may
// approach the channel's megabyte limit.
//
// The checks are ordered by what they cost us: the ones that need nothing but
// the message, then the one that needs only memory, then the height window,
// which reaches the block store.
//
// A refusal here is local and silent. It is never a peer error and never
// touches peer state: a message we decline to verify tells us nothing about
// whether its sender is honest, and the sender re-sends pending evidence every
// second, so a refusal delays evidence rather than losing it.
func (r *Reactor) admitFree(ev types.Evidence, logger log.Logger) bool {
	if !allegesOneEquivocation(ev) {
		r.refuse(logger, "structural", "refusing evidence whose votes do not describe one equivocation")
		return false
	}

	if !withinExtensionLimit(ev) {
		r.refuse(logger, "oversized", "refusing evidence carrying more vote extensions than a vote may have")
		return false
	}

	if r.evpool.hasIdentity(ev) {
		r.refuse(logger, "already_known", "refusing evidence for an equivocation we already hold evidence of")
		return false
	}

	// Outside the range the block store can serve, verification could only fail
	// on the missing block meta — after paying for the lookup.
	if !r.evpool.hasBlockFor(ev.Height()) {
		r.refuse(logger, "height_window", "refusing evidence outside the heights we can verify")
		return false
	}

	return true
}

// refuse records a refusal. Every path that declines to verify evidence goes
// through here so the reasons are countable: a shed message is invisible to the
// sender, so the receiving operator is the only one who can see it happening.
func (r *Reactor) refuse(logger log.Logger, reason, message string) {
	logger.Debug(message, "reason", reason)
	r.evpool.Metrics.DroppedEvidence.With("reason", reason).Add(1)
}

// admitWork charges an inbound piece of evidence against the work budgets and
// reports whether it may be verified. Verification costs two BLS pairings plus
// a block-meta and a validator-set lookup, none of which the sender pays for.
//
// It runs after the free refusals, so nothing that was never going to be
// verified consumes a budget: a peer re-sending evidence we hold — which every
// peer does once a second for as long as it stays pending — is never charged.
//
// Refusals here are silent and non-punitive for the same reason as in
// admitFree: being unable to afford a message says nothing about its sender.
func (r *Reactor) admitWork(ctx context.Context, from types.NodeID, logger log.Logger) bool {
	// Look at the node-wide budget before charging the sender. A message the
	// node as a whole cannot afford must not also cost the sender its own
	// allowance, or an honest peer would spend its budget on attempts that are
	// discarded anyway and its retries would slow down exactly when the channel
	// is congested. Admission runs on a single goroutine, so nothing can spend
	// the tokens between this look and the charge below.
	now := r.clock.Now()
	if r.nodeBudget != nil && r.nodeBudget.TokensAt(now) < evidenceVerifyCost {
		r.refuse(logger, "node_budget", "refusing evidence over the node-wide work budget")
		return false
	}

	if r.peerLimit != nil {
		allowed, err := r.peerLimit.Limit(ctx, from, evidenceVerifyCost)
		if err != nil {
			logger.Error("evidence rate limiter failed", "err", err)
		} else if !allowed {
			r.refuse(logger, "peer_budget", "refusing evidence over the sender's work budget")
			return false
		}
	}

	// Charging at the same instant the check above read, so this cannot fail
	// while admission stays single-goroutine — but it is what actually takes the
	// tokens, and it must stay correct if that ever changes.
	if r.nodeBudget != nil && !r.nodeBudget.AllowN(now, evidenceVerifyCost) {
		r.refuse(logger, "node_budget", "refusing evidence over the node-wide work budget")
		return false
	}

	return true
}

// allegesOneEquivocation reports whether the two votes could be the same
// validator voting twice in the same step. VerifyDuplicateVote makes the same
// comparisons, but only after loading a block meta and a validator set from
// disk; making them here keeps evidence that cannot possibly verify off the
// disk entirely.
//
// Evidence types that allege no equivocation are not this check's business and
// pass through to the pool, which rejects them on their own terms.
func allegesOneEquivocation(ev types.Evidence) bool {
	dve, ok := ev.(*types.DuplicateVoteEvidence)
	if !ok {
		return true
	}
	if dve.VoteA == nil || dve.VoteB == nil {
		return false
	}
	return dve.VoteA.Height == dve.VoteB.Height &&
		dve.VoteA.Round == dve.VoteB.Round &&
		dve.VoteA.Type == dve.VoteB.Type &&
		dve.VoteA.ValidatorProTxHash.Equal(dve.VoteB.ValidatorProTxHash) &&
		!dve.VoteA.BlockID.Equals(dve.VoteB.BlockID)
}

// withinExtensionLimit reports whether the votes carry no more vote extensions
// than a vote may legitimately have.
//
// Nothing on the evidence path reads these extensions: the signature check
// covers the block signature only, and the ABCI misbehavior report does not
// include them. They are carried along because the evidence quotes real votes,
// and a real vote is capped at MaxVoteExtensions — so anything above that cap
// is pure payload, inflating a message we have to hash and store.
//
// This bounds their count, not their size. A byte bound would need a threshold
// no application-defined extension may legitimately exceed, and guessing it too
// low would refuse genuine evidence — the one outcome worse than the flood.
func withinExtensionLimit(ev types.Evidence) bool {
	dve, ok := ev.(*types.DuplicateVoteEvidence)
	if !ok || dve.VoteA == nil || dve.VoteB == nil {
		return true
	}
	return len(dve.VoteA.VoteExtensions) <= types.MaxVoteExtensions &&
		len(dve.VoteB.VoteExtensions) <= types.MaxVoteExtensions
}

// newNodeBudget returns the node-wide evidence work budget.
func newNodeBudget() *rate.Limiter {
	return rate.NewLimiter(rate.Limit(nodeEvidenceRate), nodeEvidenceBurst)
}
