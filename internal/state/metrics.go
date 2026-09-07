package state

import (
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
}
