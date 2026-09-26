package consensus

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/dash"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

func TestReplayRejectsCommitExtensionsBeforeFinalize(t *testing.T) {
	for _, applyState := range []bool{false, true} {
		name := "application only"
		if applyState {
			name = "application and state"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			n := newCommitFixture(ctx, t, configSetup(t), types.BlockPartSizeBytes, 0)
			sd := n.node.GetStateData()
			ctx = dash.ContextWithProTxHash(ctx, n.node.privValidator.ProTxHash)
			app := &replayCommitExtensionApp{t: t, block: n.block}
			client := abciclient.NewLocalClient(log.NewNopLogger(), app)
			exec := sm.NewBlockExecutor(nil, client, nil, nil, nil, nil)
			if applyState {
				_, err := exec.ApplyBlock(ctx, sd.state, n.commit.BlockID, n.block, n.commit, types.VerifiedCommit{})
				require.ErrorContains(t, err, "commit extensions rejected")
			} else {
				replayer := NewBlockReplayer(client, nil, nil, nil, nil, exec)
				_, err := replayer.replayBlock(ctx, n.block, n.commit, sd.state, n.block.Height)
				require.ErrorContains(t, err, "commit extensions rejected")
				require.Zero(t, replayer.nBlocks)
			}
			require.Equal(t, []string{"process", "verify"}, app.calls)
		})
	}
}

type replayCommitExtensionApp struct {
	abci.BaseApplication
	t     *testing.T
	block *types.Block
	calls []string
}

func (a *replayCommitExtensionApp) ProcessProposal(_ context.Context, req *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
	a.calls = append(a.calls, "process")
	return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_ACCEPT, AppHash: a.block.AppHash,
		TxResults: factory.ExecTxResults(types.NewTxs(req.Txs))}, nil
}

func (a *replayCommitExtensionApp) VerifyVoteExtension(_ context.Context, req *abci.RequestVerifyVoteExtension) (*abci.ResponseVerifyVoteExtension, error) {
	require.Equal(a.t, []string{"process"}, a.calls)
	require.Empty(a.t, req.ValidatorProTxHash)
	require.Equal(a.t, []byte(a.block.Hash()), req.Hash)
	require.Equal(a.t, a.block.Height, req.Height)
	a.calls = append(a.calls, "verify")
	return &abci.ResponseVerifyVoteExtension{Status: abci.ResponseVerifyVoteExtension_REJECT}, nil
}

func (a *replayCommitExtensionApp) FinalizeBlock(_ context.Context, _ *abci.RequestFinalizeBlock) (*abci.ResponseFinalizeBlock, error) {
	a.calls = append(a.calls, "finalize")
	return &abci.ResponseFinalizeBlock{}, nil
}
