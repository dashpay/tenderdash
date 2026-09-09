package consensus

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/dash"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func securityThresholdExtensions(t *testing.T, values ...string) types.VoteExtensions {
	t.Helper()
	pb := make([]*tmproto.VoteExtension, len(values))
	for i, value := range values {
		pb[i] = &tmproto.VoteExtension{
			Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
			Extension: []byte(value),
		}
	}
	exts, err := types.VoteExtensionsFromProto(pb...)
	require.NoError(t, err)
	return exts
}

// This exercises the real peer-vote dispatcher, including individual BLS
// verification and ABCI VerifyVoteExtension. Every vote is valid on its own;
// one Byzantine validator signs a different, application-accepted value at
// the same extension index. Recovery nevertheless combines all shares by
// count/index and panics when the final vote arrives.
func TestRemoteExtensionMismatchDoesNotPanicConsensusDispatcher(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := newFloodHarness(ctx, t, floodHarnessArgs{validators: 10})
	stateData := h.cs.GetStateData()
	blockID := factory.MakeBlockID()

	for i, vs := range h.vss {
		extensions := securityThresholdExtensions(t, "canonical-0", "canonical-1")
		if i == 1 {
			extensions = securityThresholdExtensions(t, "canonical-0", "byzantine-value")
		}
		vote, err := vs.signVote(ctx, tmproto.PrecommitType,
			stateData.state.ChainID, blockID,
			stateData.Validators.QuorumType, stateData.Validators.QuorumHash,
			extensions)
		require.NoError(t, err)

		mi := msgInfo{
			Msg:         &VoteMessage{Vote: vote},
			PeerID:      types.NodeID(fmt.Sprintf("peer-%d", i)),
			ReceiveTime: tmtime.Now(),
		}
		runCtx := dash.ContextWithProTxHash(ctx, h.cs.privValidator.ProTxHash)
		require.NotPanics(t, func() {
			require.NoError(t, h.cs.msgDispatcher.dispatch(runCtx, &stateData, mi))
		}, "one Byzantine extension value must not crash consensus")
	}
}
