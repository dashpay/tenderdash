package evidence_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/evidence"
)

// maxConnectionSlots is how many peers can reach this node at once: every
// connection it accepts, including the upgrade slots.
const maxConnectionSlots = 68

// Verifying one piece of duplicate-vote evidence costs two BLS pairings and two
// disk lookups, and it happens on the evidence reactor's own goroutine — so it
// does not stall consensus directly, but it does take a core the consensus
// verifier is competing for.
//
// This records what an attacker holding every connection slot gets out of that,
// with evidence that nothing can de-duplicate: each piece alleges an
// equivocation the node has never seen, so only the work budget stands between
// the sender and a verification.
//
// The number to read is the pairing rate, because that is what composes with
// the consensus verification budget on the same machine.
func TestLoadEvidenceFloodAtMaxConnections(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	h := newAdmissionHarness(ctx, t)
	genuine := h.evidenceAt(ctx, t, h.height)
	verifyCost, nodeBurst, nodeRate := evidence.AdmissionBudgetsForTesting()

	// The block-store oracle counts disk lookups, and one verification makes
	// several of them, so what one costs is measured rather than assumed: a
	// hardcoded ratio would silently rescale the whole result if the
	// verification path changed.
	before := int(h.blockStore.loads.Load())
	_ = h.deliver(ctx, t, peerID(0), fabricate(t, genuine, 0))
	perVerification := int(h.blockStore.loads.Load()) - before
	require.Positive(t, perVerification, "no evidence reached verification, so nothing can be counted")

	// Frozen clock: nothing refills, so what reaches verification is what the
	// buckets held when the flood started.
	const perPeer = 200
	for peer := 0; peer < maxConnectionSlots; peer++ {
		for n := 1; n <= perPeer; n++ {
			_ = h.deliver(ctx, t, peerID(peer), fabricate(t, genuine, peer*perPeer+n))
		}
	}
	verified := (int(h.blockStore.loads.Load()) - before) / perVerification

	offered := maxConnectionSlots*perPeer + 1
	ceiling := nodeBurst / verifyCost
	reportf(t, "%d peers offered %d unrecognisable pieces of evidence, %d reached verification "+
		"(node-wide burst allows %d)", maxConnectionSlots, offered, verified, ceiling)
	reportf(t, "the evidence channel admits at most %.0f BLS pairings per second node-wide, "+
		"on top of the consensus verification budget", nodeRate)

	require.LessOrEqual(t, verified, ceiling,
		"more evidence reached verification than the node-wide budget allows, "+
			"so fresh identities buy their way past the per-peer ceiling")
	require.Positive(t, verified,
		"nothing reached verification, so genuine evidence could not get through either")
	require.Less(t, verified, offered,
		"nothing was refused, so the flood never reached the ceiling and the bound above "+
			"held only because it was never tested")
	require.Zero(t, h.ch.errorCount(),
		"refusing evidence over the budget must never be reported as its sender's fault")
}

func reportf(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Logf("MEASURED "+format, args...)
}
