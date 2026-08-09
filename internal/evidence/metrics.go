package evidence

import (
	"github.com/go-kit/kit/metrics"
)

const (
	// MetricsSubsystem is a subsystem shared by all metrics exposed by this
	// package.
	MetricsSubsystem = "evidence_pool"
)

//go:generate go run ../../scripts/metricsgen -struct=Metrics

// Metrics contains metrics exposed by this package.
// see MetricsProvider for descriptions.
type Metrics struct {
	// Number of pending evidence in the evidence pool.
	NumEvidence metrics.Gauge

	// Number of inbound evidence messages refused before verification, by
	// reason. Evidence is a safety mechanism, so refusing it silently is the
	// wrong default: this is how an operator sees a flood being shed, and how
	// they would notice a limit tuned so tightly that genuine evidence is
	// struggling to get through.
	DroppedEvidence metrics.Counter `metrics_labels:"reason"`
}
