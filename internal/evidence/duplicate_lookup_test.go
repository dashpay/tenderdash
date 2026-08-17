package evidence

import (
	"context"
	"testing"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/libs/log"
	tmrand "github.com/dashpay/tenderdash/libs/rand"
	"github.com/dashpay/tenderdash/types"
)

// countingEvidence reports how often its hash was computed.
type countingEvidence struct {
	types.Evidence
	hashes int
}

func (c *countingEvidence) Hash() []byte {
	c.hashes++
	return c.Evidence.Hash()
}

// Hashing evidence means marshaling the whole message and digesting it, and an
// inbound message may run to the channel's megabyte limit. The duplicate check
// on the inbound path runs before the work budget is consulted, so whatever it
// spends is spent on a message the sender has not paid for — and asking whether
// evidence is pending and whether it is committed is one question about one
// hash, not two.
func TestPendingAndCommittedAreOneHash(t *testing.T) {
	pool := &Pool{
		evidenceStore: dbm.NewMemDB(),
		logger:        log.NewNopLogger(),
	}
	proTxHash := crypto.ProTxHash(tmrand.Bytes(crypto.ProTxHashSize))
	ev := &countingEvidence{Evidence: testEvidence(10, 2, proTxHash, 0x11, 0x22)}

	require.False(t, pool.alreadyHave(ev))
	assert.Equal(t, 1, ev.hashes,
		"both lookups are keyed on the same hash, so it must be computed once")
}

// AddEvidence asks the same two questions moments later, on the same inbound
// message, and pays the same price for each hash. Evidence already committed is
// the case that reaches both lookups.
func TestAddEvidenceAsksBothLookupsWithOneHash(t *testing.T) {
	pool := &Pool{
		evidenceStore: dbm.NewMemDB(),
		logger:        log.NewNopLogger(),
	}
	proTxHash := crypto.ProTxHash(tmrand.Bytes(crypto.ProTxHashSize))
	ev := &countingEvidence{Evidence: testEvidence(10, 2, proTxHash, 0x11, 0x22)}
	require.NoError(t, pool.evidenceStore.Set(keyCommittedFor(ev.Height(), ev.Evidence.Hash()), []byte{}))
	ev.hashes = 0

	require.NoError(t, pool.AddEvidence(context.Background(), ev))
	assert.Equal(t, 1, ev.hashes,
		"evidence we already hold must be recognized without hashing it twice")
}
