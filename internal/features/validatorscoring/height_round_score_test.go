package validatorscoring_test

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/features/validatorscoring"
	tmtypes "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func TestProposerSelectionHR(t *testing.T) {
	const initialHeight = 1

	proTxHashes := make([]crypto.ProTxHash, 4)
	proTxHashes[0] = crypto.Checksum([]byte("avalidator_address12"))
	proTxHashes[1] = crypto.Checksum([]byte("bvalidator_address12"))
	proTxHashes[2] = crypto.Checksum([]byte("cvalidator_address12"))
	proTxHashes[3] = crypto.Checksum([]byte("dvalidator_address12"))

	vset, _ := types.GenerateValidatorSet(types.NewValSetParam(proTxHashes))

	vs, err := validatorscoring.NewProposerStrategy(types.ConsensusParams{}, vset.Copy(), initialHeight, 0, nil)
	require.NoError(t, err)

	// initialize proposers
	proposerOrder := make([]*types.Validator, 4)
	for i := 0; i < 4; i++ {
		proposerOrder[i] = vs.MustGetProposer(int64(i+initialHeight), 0)
	}

	// h for the loop
	// j for the times
	// we should go in order for ever, despite some IncrementProposerPriority with times > 1
	var (
		h int
		j uint32
	)

	// HEIGHT ROUND STRATEGY

	// With rounds strategy, we should have different order of proposers
	vs, err = validatorscoring.NewProposerStrategy(types.ConsensusParams{
		Version: types.VersionParams{
			ConsensusVersion: int32(tmtypes.VersionParams_CONSENSUS_VERSION_1),
		},
	}, vset.Copy(), 1, 0, nil)
	require.NoError(t, err)

	j = 0
	for h = 1; h <= 10000; h++ {
		got := vs.MustGetProposer(int64(h), 0).ProTxHash
		expected := proposerOrder[j%4].ProTxHash
		if !bytes.Equal(got, expected) {
			t.Fatalf("vset.Proposer (%X) does not match expected proposer (%X) for (%d, %d)", got, expected, h, j)
		}

		round := uint32(rand.Int31n(100))
		require.NoError(t, vs.UpdateScores(int64(h), int32(round)))
	}
}
