package evidence

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dashpay/tenderdash/crypto"
	tmrand "github.com/dashpay/tenderdash/libs/rand"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// TestAdmissionBudgetsAreSpendable is a guard against a tuning mistake that
// would silently delete the evidence channel rather than throttle it: a bucket
// smaller than one message's cost rejects that message forever.
func TestAdmissionBudgetsAreSpendable(t *testing.T) {
	assert.GreaterOrEqual(t, peerEvidenceBurst, evidenceVerifyCost,
		"a peer bucket below one message's cost would refuse every message forever")
	assert.GreaterOrEqual(t, nodeEvidenceBurst, evidenceVerifyCost,
		"a node bucket below one message's cost would refuse every message forever")
	assert.GreaterOrEqual(t, nodeEvidenceRate, 2*float64(assumedMaxPeers)*peerEvidenceRate,
		"the node-wide rate must exceed what every peer may spend, or peers inside "+
			"their own allowance compete for it and admission becomes a race")
}

func TestAllegesOneEquivocation(t *testing.T) {
	proTxHash := crypto.ProTxHash(tmrand.Bytes(crypto.ProTxHashSize))

	assert.True(t, allegesOneEquivocation(testEvidence(10, 2, proTxHash, 0x11, 0x22)))

	for name, mutate := range map[string]func(*types.DuplicateVoteEvidence){
		"mismatched height":    func(e *types.DuplicateVoteEvidence) { e.VoteB.Height++ },
		"mismatched round":     func(e *types.DuplicateVoteEvidence) { e.VoteB.Round++ },
		"mismatched type":      func(e *types.DuplicateVoteEvidence) { e.VoteB.Type = tmproto.PrevoteType },
		"mismatched validator": func(e *types.DuplicateVoteEvidence) { e.VoteB.ValidatorProTxHash = tmrand.Bytes(crypto.ProTxHashSize) },
		"same block":           func(e *types.DuplicateVoteEvidence) { e.VoteB.BlockID = e.VoteA.BlockID },
		"missing vote":         func(e *types.DuplicateVoteEvidence) { e.VoteB = nil },
	} {
		t.Run(name, func(t *testing.T) {
			ev := testEvidence(10, 2, proTxHash, 0x11, 0x22)
			mutate(ev)
			assert.False(t, allegesOneEquivocation(ev),
				fmt.Sprintf("%s cannot describe one validator voting twice in one step", name))
		})
	}
}
