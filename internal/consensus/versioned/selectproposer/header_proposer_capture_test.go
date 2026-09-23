package selectproposer_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	selectproposer "github.com/dashpay/tenderdash/internal/consensus/versioned/selectproposer"
	"github.com/dashpay/tenderdash/types"
)

type proposerCaptureBlockStore struct{ meta *types.BlockMeta }

func (s proposerCaptureBlockStore) Base() int64 { return 1 }
func (s proposerCaptureBlockStore) LoadBlockMeta(h int64) *types.BlockMeta {
	if h == s.meta.Header.Height {
		return s.meta
	}
	return nil
}

func TestHeaderMustNotCaptureNextProposer(t *testing.T) {
	vals, _ := types.RandValidatorSet(4)
	attacker := vals.GetByIndex(1)
	predecessor := vals.GetByIndex(0)
	expectedNext := vals.GetByIndex(2)
	for h := int64(2); h < 5; h++ {
		bs := proposerCaptureBlockStore{meta: &types.BlockMeta{Header: types.Header{
			Height: h, ValidatorsHash: vals.Hash(), ProposerProTxHash: predecessor.ProTxHash,
		}}}
		require.NoError(t, vals.SetProposer(attacker.ProTxHash))
		s, err := selectproposer.NewHeightRoundProposerSelector(vals.Copy(), h+1, 0, bs, nil)
		require.NoError(t, err)
		got, err := s.GetProposer(h+1, 0)
		require.NoError(t, err)
		if !expectedNext.ProTxHash.Equal(got.ProTxHash) {
			t.Errorf("height %d: next proposer was attacker=%t; expected its successor", h+1, attacker.ProTxHash.Equal(got.ProTxHash))
		}
	}
}
