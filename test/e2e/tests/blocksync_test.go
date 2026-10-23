package e2e_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	e2eapp "github.com/dashpay/tenderdash/test/e2e/app"
	e2e "github.com/dashpay/tenderdash/test/e2e/pkg"
)

const (
	// blockSyncStallTimeout mirrors syncTimeout in internal/blocksync. Below it
	// block sync never considers handing over to consensus, so a shorter hold
	// would prove nothing about a premature handoff.
	blockSyncStallTimeout = 60 * time.Second
	// minHeldResponseBytes is the block-response volume that must arrive while
	// the application is held, establishing local buffering behind it rather
	// than a slow source or an idle network.
	minHeldResponseBytes = 16 * 1024
	// maxHoldSampleGap bounds the gap between consecutive hold samples, which
	// the application takes every second, so the whole hold is observed.
	maxHoldSampleGap = 5 * time.Second
	// maxHoldRecovery bounds the time from release to block sync's handoff to
	// consensus, after catching up past start_at.
	maxHoldRecovery = 2 * time.Minute
)

// TestBlockSync_FinalizeHold checks that block sync survives a slow local
// application. A node with finalize_hold starts late, without state sync, and
// its application blocks inside the first FinalizeBlock for longer than the
// block-sync stall timeout while networking keeps running.
//
// While held, the node must keep buffering block responses, stay in block sync
// (no premature consensus handoff), keep its peers and record no eviction; the
// block store must stay at the held block. Within maxHoldRecovery of release it
// must send further block requests and receive further responses beyond those
// of the hold window, store blocks up to start_at and hand over to consensus,
// still without evictions; it must then keep up with the network.
//
// The application samples node metrics in-process during the hold and until the
// handoff; this test checks that record and the node's live state after the run.
func TestBlockSync_FinalizeHold(t *testing.T) {
	testNode(t, func(ctx context.Context, t *testing.T, node e2e.Node) {
		if node.FinalizeHold == 0 {
			t.Skip("application is not held")
		}
		require.Greater(t, node.FinalizeHold, blockSyncStallTimeout,
			"finalize_hold must outlast the block-sync stall timeout")

		path := filepath.Join(node.Testnet.Dir, node.Name, "data", "app", e2eapp.FinalizeHoldRecordFile)
		record, err := e2eapp.LoadFinalizeHoldRecord(path)
		require.NoError(t, err, "application was never held")
		require.Equal(t, node.Testnet.InitialHeight, record.Height, "hold must catch the first block")
		require.GreaterOrEqual(t, record.ReleasedAt.Sub(record.EnteredAt), node.FinalizeHold)

		samples := record.Samples
		require.NotEmpty(t, samples)
		require.WithinDuration(t, record.EnteredAt, samples[0].Time, maxHoldSampleGap)
		require.WithinDuration(t, record.ReleasedAt, samples[len(samples)-1].Time, maxHoldSampleGap)
		for i, s := range samples {
			require.Empty(t, s.Error, "sample %d", i)
			if i > 0 {
				require.LessOrEqual(t, s.Time.Sub(samples[i-1].Time), maxHoldSampleGap, "sample %d", i)
			}
			require.Equal(t, float64(1), s.BlockSyncing, "sample %d: handed over to consensus while held", i)
			require.GreaterOrEqual(t, s.PeersConnected, float64(2), "sample %d: lost peers while held", i)
			require.Zero(t, s.PeersEvicted, "sample %d: evicted a peer while held", i)
			require.Equal(t, record.Height, s.StoreHeight, "sample %d: block store moved while held", i)
		}
		held := samples[len(samples)-1]
		require.Greater(t, held.BlockResponseBytesReceived, float64(minHeldResponseBytes),
			"no block-response backlog behind the held application")
		require.Positive(t, held.BlockRequestBytesSent, "sent block requests are not exported")

		handoff := record.Handoff
		require.NotNil(t, handoff, "block sync never handed over to consensus after release")
		require.Empty(t, handoff.Error)
		require.LessOrEqual(t, handoff.Time.Sub(record.ReleasedAt), maxHoldRecovery,
			"slow recovery after release")
		require.GreaterOrEqual(t, handoff.StoreHeight, node.StartAt, "handed over before catching up")
		require.Zero(t, handoff.PeersEvicted, "evicted a peer during recovery")
		require.Greater(t, handoff.BlockRequestBytesSent, held.BlockRequestBytesSent,
			"no block requests after release")
		require.Greater(t, handoff.BlockResponseBytesReceived, held.BlockResponseBytesReceived,
			"no block responses after release")

		client, err := node.Client()
		require.NoError(t, err)
		status, err := client.Status(ctx)
		require.NoError(t, err)
		require.False(t, status.SyncInfo.CatchingUp, "node did not finish block sync")
		require.GreaterOrEqual(t, status.SyncInfo.LatestBlockHeight, node.StartAt)

		final, err := fetchBlockSyncMetrics(ctx, node)
		require.NoError(t, err)
		require.Zero(t, final.PeersEvicted, "evicted a peer after release")
		require.Equal(t, float64(0), final.BlockSyncing, "node returned to block sync")

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			tip := networkTip(ctx, node)
			require.Positive(c, tip, "no other node answered")
			status, err := client.Status(ctx)
			require.NoError(c, err)
			require.LessOrEqual(c, tip-status.SyncInfo.LatestBlockHeight, int64(2),
				"node is behind the network")
		}, time.Minute, time.Second)
	})
}

// networkTip returns the highest height reported by any other stateful node.
func networkTip(ctx context.Context, self e2e.Node) int64 {
	var tip int64
	for _, n := range self.Testnet.Nodes {
		if n.Name == self.Name || n.Stateless() {
			continue
		}
		client, err := n.Client()
		if err != nil {
			continue
		}
		if status, err := client.Status(ctx); err == nil {
			tip = max(tip, status.SyncInfo.LatestBlockHeight)
		}
	}
	return tip
}

// fetchBlockSyncMetrics reads the node's Prometheus endpoint, published on the
// host at ProxyPort+1000.
func fetchBlockSyncMetrics(ctx context.Context, node e2e.Node) (e2eapp.BlockSyncMetrics, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	url := fmt.Sprintf("http://127.0.0.1:%d/metrics", node.ProxyPort+1000)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return e2eapp.BlockSyncMetrics{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return e2eapp.BlockSyncMetrics{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return e2eapp.BlockSyncMetrics{}, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	parser := expfmt.NewTextParser(model.UTF8Validation)
	byName, err := parser.TextToMetricFamilies(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return e2eapp.BlockSyncMetrics{}, err
	}
	families := make([]*dto.MetricFamily, 0, len(byName))
	for _, family := range byName {
		families = append(families, family)
	}
	return e2eapp.ParseBlockSyncMetrics(families)
}
