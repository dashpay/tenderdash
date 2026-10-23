package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/dashpay/tenderdash/libs/log"
)

// FinalizeHoldRecordFile is the file, inside the application directory, where a
// held application records what the node did while FinalizeBlock was blocked.
// Its presence also disarms the hold, so a restarted node is never held twice.
const FinalizeHoldRecordFile = "finalize_hold.json"

const (
	// finalizeHoldSampleInterval matches how often block sync re-evaluates a stall.
	finalizeHoldSampleInterval = time.Second
	// finalizeHoldHandoffWatch bounds how long the node is watched after release
	// for its block-sync handoff to consensus.
	finalizeHoldHandoffWatch = 10 * time.Minute
)

// BlockSyncMetrics is one observation of the node-side metrics showing whether
// block sync kept its peers, and kept fetching, while the application was held.
// Counters are totals over all peers; an evicted-peer counter that was never
// incremented is not exported and reads as zero.
type BlockSyncMetrics struct {
	Time time.Time `json:"time"`
	// BlockSyncing is consensus_block_syncing: 1 until block sync hands over to consensus.
	BlockSyncing float64 `json:"block_syncing"`
	// PeersConnected is p2p_peers_connected.
	PeersConnected float64 `json:"peers_connected"`
	// PeersEvicted is p2p_peers_evicted.
	PeersEvicted float64 `json:"peers_evicted"`
	// BlockResponseBytesReceived sums received blocksync_BlockResponse bytes.
	BlockResponseBytesReceived float64 `json:"block_response_bytes_received"`
	// BlockRequestBytesSent sums sent blocksync_BlockRequest bytes. Only the
	// priority p2p queue exports sent bytes.
	BlockRequestBytesSent float64 `json:"block_request_bytes_sent"`
	// StoreHeight is the block store height, or -1 when no probe is installed.
	StoreHeight int64 `json:"store_height"`
	// Error is set instead of the values above when the sample failed.
	Error string `json:"error,omitempty"`
}

// FinalizeHoldRecord describes one held FinalizeBlock call.
type FinalizeHoldRecord struct {
	Height     int64         `json:"height"`
	Hold       time.Duration `json:"hold"`
	EnteredAt  time.Time     `json:"entered_at"`
	ReleasedAt time.Time     `json:"released_at"`
	// Samples cover the hold, from entry to release.
	Samples []BlockSyncMetrics `json:"samples"`
	// Handoff is the first sample after release in which block sync had handed
	// over to consensus; nil until then, or if it never happened while watched.
	Handoff *BlockSyncMetrics `json:"handoff,omitempty"`
}

// LoadFinalizeHoldRecord reads a record written by a held application.
func LoadFinalizeHoldRecord(path string) (FinalizeHoldRecord, error) {
	var record FinalizeHoldRecord
	data, err := os.ReadFile(path)
	if err != nil {
		return record, err
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return record, fmt.Errorf("decode %s: %w", path, err)
	}
	return record, nil
}

// ParseBlockSyncMetrics extracts BlockSyncMetrics from gathered metric families,
// regardless of the metrics namespace. It fails when the block-sync or
// connected-peer gauges are missing, since their absence would make every
// assertion on them vacuous.
func ParseBlockSyncMetrics(families []*dto.MetricFamily) (BlockSyncMetrics, error) {
	m := BlockSyncMetrics{BlockSyncing: -1, PeersConnected: -1, StoreHeight: -1}
	for _, family := range families {
		name := family.GetName()
		for _, metric := range family.GetMetric() {
			switch {
			case strings.HasSuffix(name, "_consensus_block_syncing"):
				m.BlockSyncing = sumGauge(m.BlockSyncing, metric)
			case strings.HasSuffix(name, "_p2p_peers_connected"):
				m.PeersConnected = sumGauge(m.PeersConnected, metric)
			case strings.HasSuffix(name, "_p2p_peers_evicted"):
				m.PeersEvicted += metric.GetCounter().GetValue()
			case strings.HasSuffix(name, "_p2p_peer_receive_bytes_total") &&
				hasLabel(metric, "message_type", "blocksync_BlockResponse"):
				m.BlockResponseBytesReceived += metric.GetCounter().GetValue()
			case strings.HasSuffix(name, "_p2p_peer_send_bytes_total") &&
				hasLabel(metric, "message_type", "blocksync_BlockRequest"):
				m.BlockRequestBytesSent += metric.GetCounter().GetValue()
			}
		}
	}
	if m.BlockSyncing < 0 || m.PeersConnected < 0 {
		return m, errors.New("missing block-sync or connected-peer metrics")
	}
	return m, nil
}

func sumGauge(acc float64, metric *dto.Metric) float64 {
	return max(acc, 0) + metric.GetGauge().GetValue()
}

func hasLabel(metric *dto.Metric, name, value string) bool {
	for _, label := range metric.GetLabel() {
		if label.GetName() == name && label.GetValue() == value {
			return true
		}
	}
	return false
}

// finalizeHold blocks the first FinalizeBlock call for a fixed duration while
// networking keeps running, sampling node metrics in-process until release and
// then, in the background, until block sync hands over to consensus.
//
// Fields other than armed and height are immutable once SetFinalizeHold returns,
// so FinalizeBlock and the handoff watcher share them without locking. armed
// makes the hold one-shot; height is installed by the node after the
// application exists, possibly while a sample is being taken.
type finalizeHold struct {
	duration time.Duration
	interval time.Duration
	watch    time.Duration
	record   string
	logger   log.Logger
	gatherer prometheus.Gatherer
	armed    atomic.Bool
	height   atomic.Pointer[func() int64]
}

// SetFinalizeHold arms a one-shot hold of the first FinalizeBlock call. The
// record is written to dir; an existing record keeps the hold disarmed.
func (app *Application) SetFinalizeHold(duration time.Duration, dir string) error {
	if duration <= 0 {
		app.hold = nil
		return nil
	}
	hold := &finalizeHold{
		duration: duration,
		interval: finalizeHoldSampleInterval,
		watch:    finalizeHoldHandoffWatch,
		record:   filepath.Join(dir, FinalizeHoldRecordFile),
		logger:   app.logger,
		gatherer: prometheus.DefaultGatherer,
	}
	if hold.logger == nil {
		hold.logger = log.NewNopLogger()
	}
	switch _, err := os.Stat(hold.record); {
	case errors.Is(err, os.ErrNotExist):
		hold.armed.Store(true)
	case err != nil:
		return fmt.Errorf("check finalize hold record: %w", err)
	}
	app.hold = hold
	return nil
}

// SetBlockStoreHeight installs the probe used to record the block store height
// while FinalizeBlock is held.
func (app *Application) SetBlockStoreHeight(height func() int64) {
	if app.hold != nil {
		app.hold.height.Store(&height)
	}
}

// wait holds a single FinalizeBlock call and records what the node did
// meanwhile. After release, it keeps watching in the background for block
// sync's handoff to consensus and rewrites the record when it happens.
func (h *finalizeHold) wait(ctx context.Context, height int64) error {
	if h == nil || !h.armed.CompareAndSwap(true, false) {
		return nil
	}
	h.logger.Info("holding FinalizeBlock", "height", height, "hold", h.duration)
	record := FinalizeHoldRecord{Height: height, Hold: h.duration, EnteredAt: time.Now()}
	record.Samples = append(record.Samples, h.sample())
	release := time.NewTimer(h.duration)
	defer release.Stop()
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()
	for held := true; held; {
		select {
		case <-ctx.Done():
			// No record: the node is shutting down mid-hold.
			return ctx.Err()
		case <-ticker.C:
		case <-release.C:
			held = false
		}
		record.Samples = append(record.Samples, h.sample())
	}
	record.ReleasedAt = time.Now()
	if err := writeFinalizeHoldRecord(h.record, record); err != nil {
		return err
	}
	h.logger.Info("released FinalizeBlock", "height", height, "record", h.record)
	// The watcher owns record from here on; FinalizeBlock must not wait for it.
	go h.watchHandoff(record)
	return nil
}

// watchHandoff records the first sample showing that block sync handed over to
// consensus, or gives up after h.watch, leaving the record without a handoff.
// The goroutine lives at most h.watch: the application has no stop hook, and
// a node that is stopped sooner simply leaves no handoff behind.
func (h *finalizeHold) watchHandoff(record FinalizeHoldRecord) {
	deadline := time.NewTimer(h.watch)
	defer deadline.Stop()
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			h.logger.Error("no block-sync handoff after FinalizeBlock hold", "watched", h.watch)
			return
		case <-ticker.C:
		}
		if m := h.sample(); m.Error == "" && m.BlockSyncing == 0 {
			record.Handoff = &m
			// A failed write leaves the record without a handoff, which the
			// e2e test reports; the node itself must keep running.
			if err := writeFinalizeHoldRecord(h.record, record); err != nil {
				h.logger.Error("cannot record block-sync handoff", "err", err)
			}
			return
		}
	}
}

func (h *finalizeHold) sample() BlockSyncMetrics {
	now := time.Now()
	families, err := h.gatherer.Gather()
	if err != nil {
		return BlockSyncMetrics{Time: now, Error: err.Error()}
	}
	m, err := ParseBlockSyncMetrics(families)
	m.Time = now
	if err != nil {
		m.Error = err.Error()
	}
	if probe := h.height.Load(); probe != nil {
		m.StoreHeight = (*probe)()
	}
	return m
}

// writeFinalizeHoldRecord replaces the record atomically, so a reader never
// sees a partial file.
func writeFinalizeHoldRecord(path string, record FinalizeHoldRecord) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil { //nolint:gosec // G306: read by the e2e tests on the host
		return fmt.Errorf("write finalize hold record: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write finalize hold record: %w", err)
	}
	return nil
}
