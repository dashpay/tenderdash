package selectproposer_test

import (
	"bytes"
	"math/big"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	selectproposer "github.com/dashpay/tenderdash/internal/consensus/versioned/selectproposer"
	"github.com/dashpay/tenderdash/libs/log"
	tmtypes "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func TestProposerSelectionHR(t *testing.T) {
	const nVals = 4
	const initialHeight = 1

	proTxHashes := make([]crypto.ProTxHash, 0, nVals)
	for i := 0; i < nVals; i++ {
		protx := make([]byte, crypto.ProTxHashSize)
		big.NewInt(int64(i + 1)).FillBytes(protx)
		proTxHashes = append(proTxHashes, protx)
	}

	vset, _ := types.GenerateValidatorSet(types.NewValSetParam(proTxHashes))

	// initialize proposers
	proposerOrder := make([]*types.Validator, vset.Size())
	for i := 0; i < vset.Size(); i++ {
		proposerOrder[i] = vset.Validators[i]
	}

	var (
		h             int
		proposerIndex uint32
	)

	// HEIGHT ROUND STRATEGY

	vs, err := selectproposer.NewProposerSelector(types.ConsensusParams{
		Version: types.VersionParams{
			ConsensusVersion: int32(tmtypes.VersionParams_CONSENSUS_VERSION_1),
		},
	}, vset.Copy(), initialHeight, 0, nil, log.NewTestingLogger(t))
	require.NoError(t, err)

	proposerIndex = 0
	for h = 1; h <= 10000; h++ {
		got := vs.MustGetProposer(int64(h), 0).ProTxHash
		expected := proposerOrder[proposerIndex%nVals].ProTxHash
		if !bytes.Equal(got, expected) {
			t.Fatalf("vset.Proposer (%X) does not match expected proposer (%X) for (%d, %d)", got, expected, h, proposerIndex)
		}

		round := uint32(rand.Int31n(100))
		require.NoError(t, vs.UpdateHeightRound(int64(h), int32(round)))

		// t.Logf("Height: %d, Round: %d, proposer index %d", h, round, proposerIndex)
		// we expect proposer increase for each round, plus one for next height
		proposerIndex += 1 + round
	}
}
