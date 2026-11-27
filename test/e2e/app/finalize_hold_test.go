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

// testGauges are the node gauges a test drives while the application is held.
type testGauges struct {
	syncing, peers prometheus.Gauge
}

// newTestHold builds an armed hold over a private registry exposing the metrics
// the node exports, so tests never touch the process-wide default registry.
func newTestHold(t *testing.T, duration time.Duration) (*Application, testGauges, string) {
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
	app.hold.readyTimeout = time.Second
	return app, testGauges{syncing: syncing, peers: peers}, filepath.Join(dir, FinalizeHoldRecordFile)
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
	app, gauges, path := newTestHold(t, 20*time.Millisecond)
	app.SetBlockStoreHeight(func() int64 { return 1100 })
	require.NoError(t, app.hold.wait(context.Background(), 1000))
	released, err := LoadFinalizeHoldRecord(path)
	require.NoError(t, err)

	gauges.syncing.Set(0)
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

// The measured hold starts only once FinalizeBlockHoldMinPeers sources are
// connected, so a startup that applies the first block from a single source
// does not record a lost peer, and the window still lasts the full hold.
func TestFinalizeHoldWaitsForSources(t *testing.T) {
	const hold = 100 * time.Millisecond
	app, gauges, path := newTestHold(t, hold)
	gauges.peers.Set(1)
	connect := time.AfterFunc(50*time.Millisecond, func() { gauges.peers.Set(FinalizeHoldMinPeers) })
	defer connect.Stop()

	require.NoError(t, app.hold.wait(context.Background(), 1000))
	record, err := LoadFinalizeHoldRecord(path)
	require.NoError(t, err)
	require.Empty(t, record.ReadinessError)
	require.GreaterOrEqual(t, record.ReadyAt.Sub(record.EnteredAt), 40*time.Millisecond, "hold started before sources connected")
	require.GreaterOrEqual(t, record.ReleasedAt.Sub(record.ReadyAt), hold, "measured window shortened by the readiness wait")
	require.NotEmpty(t, record.Samples)
	require.False(t, record.Samples[0].Time.Before(record.ReadyAt))
	for i, s := range record.Samples {
		require.GreaterOrEqual(t, s.PeersConnected, float64(FinalizeHoldMinPeers), "sample %d", i)
	}
}

// A node that never reaches FinalizeHoldMinPeers sources is not held at all and
// says so in the record, instead of recording a hold that proves nothing.
func TestFinalizeHoldReadinessTimeout(t *testing.T) {
	app, gauges, path := newTestHold(t, time.Hour)
	gauges.peers.Set(1)
	app.hold.readyTimeout = 50 * time.Millisecond

	start := time.Now()
	require.NoError(t, app.hold.wait(context.Background(), 1000))
	require.Less(t, time.Since(start), time.Minute, "application held without enough sources")
	record, err := LoadFinalizeHoldRecord(path)
	require.NoError(t, err)
	require.NotEmpty(t, record.ReadinessError)
	require.Empty(t, record.Samples)
}

// WaitFinalizeHoldHandoff rereads the record until the asynchronously written
// handoff appears, instead of judging a release-only record.
func TestWaitFinalizeHoldHandoff(t *testing.T) {
	path := filepath.Join(t.TempDir(), FinalizeHoldRecordFile)
	released := FinalizeHoldRecord{Height: 1, ReleasedAt: time.Now()}
	require.NoError(t, writeFinalizeHoldRecord(path, released))
	write := time.AfterFunc(100*time.Millisecond, func() {
		withHandoff := released
		withHandoff.Handoff = &BlockSyncMetrics{Time: time.Now()}
		_ = writeFinalizeHoldRecord(path, withHandoff)
	})
	defer write.Stop()

	record, err := WaitFinalizeHoldHandoff(context.Background(), path, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, record.Handoff)
}

func TestWaitFinalizeHoldHandoffDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), FinalizeHoldRecordFile)
	// Released long ago: the recovery allowance has expired.
	require.NoError(t, writeFinalizeHoldRecord(path, FinalizeHoldRecord{Height: 1, ReleasedAt: time.Now().Add(-time.Hour)}))

	start := time.Now()
	record, err := WaitFinalizeHoldHandoff(context.Background(), path, time.Minute)
	require.NoError(t, err)
	require.Nil(t, record.Handoff)
	require.Less(t, time.Since(start), 10*time.Second)

	_, err = WaitFinalizeHoldHandoff(context.Background(), filepath.Join(t.TempDir(), "missing"), time.Minute)
	require.Error(t, err, "a missing record is an error, not a wait")
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
	app, _, path := newTestHold(t, time.Hour)
	app.hold.gatherer = prometheus.NewRegistry()
	app.hold.readyTimeout = 50 * time.Millisecond
	require.NoError(t, app.hold.wait(context.Background(), 1))
	record, err := LoadFinalizeHoldRecord(path)
	require.NoError(t, err)
	require.Contains(t, record.ReadinessError, "missing block-sync or connected-peer metrics",
		"missing gauges must not read as healthy")
	require.Empty(t, record.Samples)

	// A sample taken without a store probe says so.
	require.Equal(t, int64(-1), app.hold.sample().StoreHeight)
}
