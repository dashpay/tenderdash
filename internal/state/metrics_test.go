package state

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/test/metricspy"
)

// TestStageTimerAttributesEachIntervalOnce checks that consecutive done calls
// carve the elapsed time into non-overlapping intervals, each recorded under
// its own stage label in milliseconds, so the stage histograms sum to the
// whole apply rather than counting early stages several times.
func TestStageTimerAttributesEachIntervalOnce(t *testing.T) {
	h := metricspy.NewHistogram("stage")
	m := NopMetrics()
	m.BlockApplyStageDuration = h

	timer := m.startStages()
	start := timer.last
	timer.done("first")
	firstDone := timer.last
	timer.done("second")
	secondDone := timer.last

	require.Len(t, h.Samples["first"], 1)
	require.Len(t, h.Samples["second"], 1)
	require.Equal(t, float64(firstDone.Sub(start))/float64(time.Millisecond), h.Samples["first"][0])
	require.Equal(t, float64(secondDone.Sub(firstDone))/float64(time.Millisecond), h.Samples["second"][0])
}

// TestStageTimerNopMetricsIsSilent guards the default: with NopMetrics the
// stage timer costs two clock reads and records nothing.
func TestStageTimerNopMetricsIsSilent(t *testing.T) {
	timer := NopMetrics().startStages()
	require.NotPanics(t, func() { timer.done("anything") })
}
