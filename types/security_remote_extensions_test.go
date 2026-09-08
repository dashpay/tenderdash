package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

func TestSecurityRemoteExtensionRecovery(t *testing.T) {
	for _, mode := range []string{"control", "signed_same_count_different_content", "reorder_signed_extensions"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			vs, vals, pvs := randVoteSet(ctx, t, 10, 0, tmproto.PrecommitType, 10)
			blockID := makeBlockIDRandom()
			require.NotPanics(t, func() {
				for i, pv := range pvs {
					id, err := pv.GetProTxHash(ctx)
					require.NoError(t, err)
					v := &Vote{ValidatorProTxHash: id, ValidatorIndex: int32(i), Height: 10, Round: 0, Type: tmproto.PrecommitType, BlockID: blockID}
					v.VoteExtensions = thresholdVoteExtensionsOfLen(t, 2)
					if mode == "signed_same_count_different_content" && i == 0 {
						v.VoteExtensions[1].(*ThresholdVoteExtension).Extension = []byte("different message signed by minority validator")
					}
					signVote(ctx, t, pv, vs.ChainID(), vals.QuorumType, vals.QuorumHash, v, log.NewNopLogger())
					if mode == "reorder_signed_extensions" && i == 0 {
						v.VoteExtensions[0], v.VoteExtensions[1] = v.VoteExtensions[1], v.VoteExtensions[0]
					}
					require.NoError(t, v.ValidateBasic())
					require.NoError(t, v.Verify(vs.ChainID(), vals.QuorumType, vals.QuorumHash, vals.GetByIndex(int32(i)).PubKey, id))
					added, err := vs.AddVote(v)
					require.NoError(t, err)
					require.True(t, added)
				}
			}, "remote extensions must not crash the receiver")
			require.True(t, vs.HasTwoThirdsMajority(), "honest quorum must recover despite minority/relay modifications")
		})
	}
}
