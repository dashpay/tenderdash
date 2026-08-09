package client

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

// The proposed L1 limiter is keyed on p2p node ID. A node ID is
// hex(SHA256(ed25519 pubkey)[:20]) -- see types.NodeIDFromPubKey -- so minting
// a fresh identity is one Ed25519 keygen, and PeerManager.Errored evicts
// without banning while PeerManager.Accepted actively clears prior penalties.
//
// Each fresh identity gets a brand-new rate.Limiter that starts with a FULL
// bucket of burst = DefaultRecvBurstMultiplier * limit = 10 * limit tokens.
// So the per-peer budget is not a per-attacker budget: it is a per-keypair
// budget, and keypairs are free.
func TestAudit_RateLimitBudgetResetsPerNodeIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const limit = 50.0
	rl := NewRateLimit(ctx, limit, true /* drop */, log.NewNopLogger())
	burst := rl.burst
	require.Equal(t, int(DefaultRecvBurstMultiplier*limit), burst)

	freshNodeID := func() types.NodeID {
		b := make([]byte, 20)
		_, _ = rand.Read(b)
		return types.NodeID(hex.EncodeToString(b))
	}

	// One identity: drain the bucket, then get throttled. This is the
	// behaviour the spec is relying on.
	victimBudget := func(id types.NodeID) int {
		n := 0
		for {
			allowed, err := rl.Limit(ctx, id, 1)
			require.NoError(t, err)
			if !allowed {
				return n
			}
			n++
			if n > burst*2 {
				t.Fatal("limiter never throttled")
			}
		}
	}

	first := freshNodeID()
	spent := victimBudget(first)
	assert.InDelta(t, burst, spent, 2, "a single identity is capped at ~burst messages up front")

	// Now rotate. Each new keypair is a new bucket, full from the first message.
	const identities = 200
	total := spent
	for i := 0; i < identities; i++ {
		total += victimBudget(freshNodeID())
	}

	t.Logf("one identity bought %d messages; %d identities bought %d messages (%.0fx)",
		spent, identities+1, total, float64(total)/float64(spent))

	assert.Greater(t, total, spent*identities,
		"rotating node keys multiplies the per-peer budget by the number of identities; "+
			"nothing in the limiter, the peer manager, or the conn tracker binds identities to a scarce resource")
}
