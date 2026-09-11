package state

import (
	"time"

	"github.com/go-kit/kit/metrics"
)

const (
	// MetricsSubsystem is a subsystem shared by all metrics exposed by this
	// package.
	MetricsSubsystem = "state"
)

//go:generate go run ../../scripts/metricsgen -struct=Metrics

// Metrics contains metrics exposed by this package.
type Metrics struct {
	// Time between BeginBlock and EndBlock.
	BlockProcessingTime metrics.Histogram `metrics_buckettype:"lin" metrics_bucketsizes:"1,10,10"`

	// ConsensusParamUpdates is the total number of times the application has
	// udated the consensus params since process start.
	//metrics:Number of consensus parameter updates returned by the application since process start.
	ConsensusParamUpdates metrics.Counter

	// ValidatorSetUpdates is the total number of times the application has
	// udated the validator set since process start.
	//metrics:Number of validator set updates returned by the application since process start.
	ValidatorSetUpdates metrics.Counter

	// LastCommitVerificationSkipped counts the LastCommit threshold verifications
	// that were skipped because VerifyCommit had already verified the identical
	// commit against the identical inputs. During block sync this should track
	// the block rate; in consensus it stays flat.
	//metrics:Number of LastCommit threshold verifications skipped because the same commit was already verified.
	LastCommitVerificationSkipped metrics.Counter

	// BlockApplyStageDuration is the wall-clock cost of each stage a committed
	// block goes through in ProcessProposal and FinalizeBlock. During block sync
	// those stages are most of a block; this says which one a slow sync is in.
	//metrics:Time spent in each stage of applying a committed block, in milliseconds.
	BlockApplyStageDuration metrics.Histogram `metrics_labels:"stage" metrics_buckettype:"exprange" metrics_bucketsizes:"0.01, 1000, 12"`
}

// stageTimer attributes the time between successive calls to done to a named
// stage of BlockApplyStageDuration.
type stageTimer struct {
	h    metrics.Histogram
	last time.Time
}

// startStages begins timing stages of a block apply; the first stage is
// measured from this moment.
func (m *Metrics) startStages() *stageTimer {
	return &stageTimer{h: m.BlockApplyStageDuration, last: time.Now()}
}

// done records the time since the previous stage ended under stage, and starts
// the next one.
func (t *stageTimer) done(stage string) {
	now := time.Now()
	t.h.With("stage", stage).Observe(float64(now.Sub(t.last)) / float64(time.Millisecond))
	t.last = now
}
