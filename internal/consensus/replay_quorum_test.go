package consensus

import (
	"context"
	"testing"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/require"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/dash"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

func TestReplayUsesHistoricalQuorumAfterRotation(t *testing.T) {
	for _, scenario := range []string{"same quorum", "rotated quorum", "missing historical validators"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := newCommitFixture(ctx, t, configSetup(t), types.BlockPartSizeBytes, 0)
			ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
			latest := f.node.GetStateData().state.Copy()
			latest.LastBlockHeight = f.block.Height + 2
			if scenario != "same quorum" {
				latest.Validators.QuorumHash = crypto.Checksum([]byte("new quorum"))
				require.NotEqual(t, f.commit.QuorumHash, latest.Validators.QuorumHash)
			}
			latestQuorum := latest.Validators.QuorumHash.Copy()

			app := &replayQuorumApp{t: t, block: f.block, quorumHash: f.commit.QuorumHash}
			client := abciclient.NewLocalClient(log.NewNopLogger(), app)
			exec := sm.NewBlockExecutor(nil, client, nil, nil, nil, nil)
			f.node.blockStore.SaveBlock(f.block, f.parts, f.commit)
			history := f.node.stateStore
			if scenario == "missing historical validators" {
				history = sm.NewStore(dbm.NewMemDB())
			}
			replayer := NewBlockReplayer(client, history, f.node.blockStore, nil, nil, exec)

			_, err := replayer.replayBlock(ctx, f.block, f.commit, latest, f.block.Height)
			if scenario == "missing historical validators" {
				var missing sm.ErrNoValSetForHeight
				require.ErrorAs(t, err, &missing)
				require.False(t, app.processed)
				require.False(t, app.finalized)
			} else {
				require.NoError(t, err)
				require.True(t, app.finalized)
			}
			require.EqualValues(t, latestQuorum, latest.Validators.QuorumHash, "replay must preserve the latest state")
		})
	}
}

type replayQuorumApp struct {
	abci.BaseApplication
	t          *testing.T
	block      *types.Block
	quorumHash crypto.QuorumHash
	processed  bool
	finalized  bool
}

func (a *replayQuorumApp) ProcessProposal(_ context.Context, req *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
	a.processed = true
	require.EqualValues(a.t, a.quorumHash, req.QuorumHash,
		"withdrawal transactions must be rebuilt with the quorum that signed this block")
	return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_ACCEPT, AppHash: a.block.AppHash,
		TxResults: factory.ExecTxResults(types.NewTxs(req.Txs))}, nil
}

func (a *replayQuorumApp) FinalizeBlock(_ context.Context, req *abci.RequestFinalizeBlock) (*abci.ResponseFinalizeBlock, error) {
	require.EqualValues(a.t, a.quorumHash, req.Commit.QuorumHash)
	a.finalized = true
	return &abci.ResponseFinalizeBlock{}, nil
}
