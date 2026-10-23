package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

// newTestHold builds an armed hold over a private registry exposing the metrics
// the node exports, so tests never touch the process-wide default registry.
func newTestHold(t *testing.T, duration time.Duration) (*Application, prometheus.Gauge, string) {
	t.Helper()
	dir := t.TempDir()
	app := &Application{}
	require.NoError(t, app.SetFinalizeHold(duration, dir))
	reg := prometheus.NewRegistry()
	syncing := prometheus.NewGauge(prometheus.GaugeOpts{Namespace: "tendermint", Subsystem: "consensus", Name: "block_syncing"})
	peers := prometheus.NewGauge(prometheus.GaugeOpts{Namespace: "tendermint", Subsystem: "p2p", Name: "peers_connected"})
	recv := prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "tendermint", Subsystem: "p2p", Name: "peer_receive_bytes_total"}, []string{"peer_id", "message_type"})
	sent := prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "tendermint", Subsystem: "p2p", Name: "peer_send_bytes_total"}, []string{"peer_id", "message_type"})
	reg.MustRegister(syncing, peers, recv, sent)
	syncing.Set(1)
	peers.Set(3)
	recv.WithLabelValues("a", "blocksync_BlockResponse").Add(1000)
	recv.WithLabelValues("b", "blocksync_BlockResponse").Add(2000)
	recv.WithLabelValues("a", "blocksync_NoBlockResponse").Add(9000)
	sent.WithLabelValues("a", "blocksync_BlockRequest").Add(40)
	sent.WithLabelValues("a", "blocksync_StatusRequest").Add(8000)
	app.hold.gatherer = reg
	app.hold.interval = 10 * time.Millisecond
	app.hold.watch = time.Second
	return app, syncing, filepath.Join(dir, FinalizeHoldRecordFile)
}

func TestFinalizeHoldRecordsHeldCall(t *testing.T) {
	const hold = 100 * time.Millisecond
	app, _, path := newTestHold(t, hold)
	app.SetBlockStoreHeight(func() int64 { return 1000 })

	start := time.Now()
	require.NoError(t, app.hold.wait(context.Background(), 1000))
	require.GreaterOrEqual(t, time.Since(start), hold)

	record, err := LoadFinalizeHoldRecord(path)
	require.NoError(t, err)
	require.Equal(t, int64(1000), record.Height)
	require.Equal(t, hold, record.Hold)
	require.GreaterOrEqual(t, record.ReleasedAt.Sub(record.EnteredAt), hold)
	require.GreaterOrEqual(t, len(record.Samples), 3, "entry, periodic and release samples")
	for _, s := range record.Samples {
		require.Empty(t, s.Error)
		require.Equal(t, float64(1), s.BlockSyncing)
		require.Equal(t, float64(3), s.PeersConnected)
		require.Equal(t, float64(3000), s.BlockResponseBytesReceived, "only block responses count")
		require.Equal(t, float64(40), s.BlockRequestBytesSent, "only block requests count")
		require.Zero(t, s.PeersEvicted, "an unincremented counter is not exported")
		require.Equal(t, int64(1000), s.StoreHeight)
	}

	require.Nil(t, record.Handoff, "block sync has not handed over yet")

	// One-shot: the next call passes straight through.
	start = time.Now()
	require.NoError(t, app.hold.wait(context.Background(), 1001))
	require.Less(t, time.Since(start), hold)
}

func TestFinalizeHoldRecordsHandoff(t *testing.T) {
	app, syncing, path := newTestHold(t, 20*time.Millisecond)
	app.SetBlockStoreHeight(func() int64 { return 1100 })
	require.NoError(t, app.hold.wait(context.Background(), 1000))
	released, err := LoadFinalizeHoldRecord(path)
	require.NoError(t, err)

	syncing.Set(0)
	var record FinalizeHoldRecord
	require.Eventually(t, func() bool {
		record, err = LoadFinalizeHoldRecord(path)
		return err == nil && record.Handoff != nil
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, released.Samples, record.Samples, "hold samples are kept")
	require.Zero(t, record.Handoff.BlockSyncing)
	require.Equal(t, int64(1100), record.Handoff.StoreHeight)
	require.False(t, record.Handoff.Time.Before(record.ReleasedAt))
}

func TestFinalizeHoldRecordDisarmsRestartedNode(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, FinalizeHoldRecordFile), []byte("{}"), 0600))
	app := &Application{}
	require.NoError(t, app.SetFinalizeHold(time.Hour, dir))
	require.NoError(t, app.hold.wait(context.Background(), 1))
}

func TestFinalizeHoldCancel(t *testing.T) {
	app, _, path := newTestHold(t, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.hold.wait(ctx, 1) }()
	select {
	case err := <-done:
		t.Fatalf("application was not held: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("application did not resume")
	}
	require.NoFileExists(t, path, "an interrupted hold leaves no record")
}

func TestFinalizeHoldDisabled(t *testing.T) {
	app := &Application{}
	require.NoError(t, app.SetFinalizeHold(0, t.TempDir()))
	require.Nil(t, app.hold)
	app.SetBlockStoreHeight(func() int64 { return 1 })
	require.NoError(t, app.hold.wait(context.Background(), 1))
}

func TestFinalizeHoldMissingInstrumentation(t *testing.T) {
	app, _, path := newTestHold(t, time.Millisecond)
	app.hold.gatherer = prometheus.NewRegistry()
	app.hold.watch = 50 * time.Millisecond
	require.NoError(t, app.hold.wait(context.Background(), 1))
	record, err := LoadFinalizeHoldRecord(path)
	require.NoError(t, err)
	require.NotEmpty(t, record.Samples)
	for _, s := range record.Samples {
		require.NotEmpty(t, s.Error, "missing gauges must not read as healthy")
		require.Equal(t, int64(-1), s.StoreHeight, "no probe installed")
	}
}
