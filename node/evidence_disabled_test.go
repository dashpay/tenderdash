package node

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/evidence"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/rpc/coretypes"
	"github.com/dashpay/tenderdash/types"
)

func TestNodeEvidenceDisabledStartupAndRestart(t *testing.T) {
	cfg, err := config.ResetTestRoot(t.TempDir(), t.Name())
	require.NoError(t, err)
	cfg.RPC.ListenAddress = ""
	cfg.DBBackend = "goleveldb"
	genDoc, err := types.GenesisDocFromFile(cfg.GenesisFile())
	require.NoError(t, err)
	genDoc.ConsensusParams.Evidence.MaxBytes = 0
	require.NoError(t, genDoc.SaveAs(cfg.GenesisFile()))
	ev, err := types.NewMockDuplicateVoteEvidence(
		t.Context(),
		1,
		time.Now(),
		genDoc.ChainID,
		genDoc.QuorumType,
		genDoc.QuorumHash,
	)
	require.NoError(t, err)

	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			// Persisted consensus state takes precedence over a local genesis edit.
			genDoc.ConsensusParams.Evidence.MaxBytes = 1000
			require.NoError(t, genDoc.SaveAs(cfg.GenesisFile()))
			require.NoError(t, os.Mkdir(filepath.Join(cfg.DBDir(), "evidence.db"), 0700))
			require.NoError(t, os.WriteFile(filepath.Join(cfg.DBDir(), "evidence.db", "untouched"), []byte("old evidence"), 0600))
		}
		func() {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			ns, err := newDefaultNode(ctx, cfg, log.NewNopLogger())
			require.NoError(t, err)
			n := ns.(*nodeImpl)
			if attempt > 0 {
				require.Positive(
					t,
					n.initialState.LastBlockHeight,
					"restart must use persisted chain state",
				)
			}
			defer func() { cancel(); n.Wait() }()
			require.NoError(t, n.Start(ctx))
			require.Eventually(
				t,
				func() bool {
					state, err := n.stateStore.Load()
					return err == nil && state.LastBlockHeight > n.initialState.LastBlockHeight
				},
				10*time.Second,
				10*time.Millisecond,
			)
			entries, err := os.ReadDir(cfg.DBDir())
			require.NoError(t, err)
			for _, entry := range entries {
				if attempt > 0 && entry.Name() == "evidence.db" {
					oldDB, err := os.ReadDir(filepath.Join(cfg.DBDir(), entry.Name()))
					require.NoError(t, err)
					require.Len(t, oldDB, 1, "disabled restart must not open an existing evidence database")
					require.Equal(t, "untouched", oldDB[0].Name())
					continue
				}
				require.NotContains(
					t,
					entry.Name(),
					"evidence",
					"disabled startup must never create an evidence database",
				)
			}
			for _, reactor := range n.services {
				_, isEvidence := reactor.(*evidence.Reactor)
				require.False(
					t,
					isEvidence,
					"disabled startup must not construct an evidence reactor",
				)
			}
			require.NotContains(
				t,
				n.NodeInfo().Channels.ToSlice(),
				uint16(evidence.EvidenceChannel),
			)
			result, err := n.RPCEnvironment().
				BroadcastEvidence(ctx, &coretypes.RequestBroadcastEvidence{Evidence: ev})
			require.Nil(t, result)
			require.ErrorContains(t, err, "evidence is disabled")
			state, err := n.stateStore.Load()
			require.NoError(t, err)
			require.Zero(t, state.ConsensusParams.Evidence.MaxBytes)
			for height := n.blockStore.Base(); height <= n.blockStore.Height(); height++ {
				require.Empty(t, n.blockStore.LoadBlock(height).Evidence)
			}
		}()
	}
}

func TestCreateEvidenceDisabledSkipsResources(t *testing.T) {
	reactor, pool, closeDB, err := createEvidenceReactor(
		log.NewNopLogger(),
		config.TestConfig(),
		types.EvidenceParams{},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	require.NoError(t, err)
	require.Nil(t, reactor)
	require.IsType(t, evidence.DisabledPool{}, pool)
	require.NoError(t, pool.Start(sm.State{}))
	require.NoError(t, closeDB())
}

func TestEvidenceChannelMatchesChainMode(t *testing.T) {
	for _, mode := range []string{config.ModeValidator, config.ModeFull, config.ModeSeed} {
		t.Run(mode, func(t *testing.T) {
			cfg := config.TestConfig()
			cfg.Mode = mode
			for _, maxBytes := range []int64{0, 1000} {
				params := types.DefaultEvidenceParams()
				params.MaxBytes = maxBytes
				wantEvidence := maxBytes > 0 && mode != config.ModeSeed
				found := false
				for _, desc := range channelDescriptors(cfg, params) {
					if desc.ID == evidence.EvidenceChannel {
						found = true
					}
				}
				require.Equal(t, wantEvidence, found)
				require.Equal(t, wantEvidence, slices.Contains(advertisedChannels(cfg, params), uint16(evidence.EvidenceChannel)))
			}
		})
	}
}
