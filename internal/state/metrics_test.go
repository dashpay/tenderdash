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
	time.Sleep(20 * time.Millisecond)
	timer.done("first")
	time.Sleep(2 * time.Millisecond)
	timer.done("second")

	require.Len(t, h.Samples["first"], 1)
	require.Len(t, h.Samples["second"], 1)
	require.GreaterOrEqual(t, h.Samples["first"][0], 20.0, "first stage covers its own sleep, in ms")
	require.Less(t, h.Samples["second"][0], 20.0, "second stage must not include the first")
}

// TestStageTimerNopMetricsIsSilent guards the default: with NopMetrics the
// stage timer costs two clock reads and records nothing.
func TestStageTimerNopMetricsIsSilent(t *testing.T) {
	timer := NopMetrics().startStages()
	require.NotPanics(t, func() { timer.done("anything") })
}
