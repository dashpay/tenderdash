package core

import (
	"testing"
	"time"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/evidence"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/state/mocks"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/rpc/coretypes"
	"github.com/dashpay/tenderdash/types"
)

func TestBroadcastEvidence(t *testing.T) {
	ev, err := types.NewMockDuplicateVoteEvidence(
		t.Context(),
		1,
		time.Now(),
		"test",
		btcjson.LLMQType_5_60,
		crypto.RandQuorumHash(),
	)
	require.NoError(t, err)
	for _, disabled := range []bool{false, true} {
		name := "enabled"
		if disabled {
			name = "disabled"
		}
		t.Run(name, func(t *testing.T) {
			disabledPool := evidence.NewPool(log.NewNopLogger(), nil, nil, nil, evidence.NopMetrics(), nil)
			require.NoError(t, disabledPool.Start(sm.State{}))
			var pool sm.EvidencePool = disabledPool
			if !disabled {
				enabled := mocks.NewEvidencePool(t)
				enabled.EXPECT().AddEvidence(t.Context(), ev).Return(nil).Once()
				pool = enabled
			}
			env := Environment{EvidencePool: pool}
			res, err := env.BroadcastEvidence(
				t.Context(),
				&coretypes.RequestBroadcastEvidence{Evidence: ev},
			)
			if disabled {
				require.ErrorIs(t, err, types.ErrEvidenceDisabled)
				require.Nil(t, res)
			} else {
				require.NoError(t, err)
				require.Equal(t, ev.Hash(), res.Hash)
			}
			_, err = env.BroadcastEvidence(t.Context(), &coretypes.RequestBroadcastEvidence{})
			require.ErrorIs(t, err, coretypes.ErrInvalidRequest)
		})
	}
}
