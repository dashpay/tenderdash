package consensus

import (
	"strings"
	"time"

	"github.com/go-kit/kit/metrics"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

const (
	// MetricsSubsystem is a subsystem shared by all metrics exposed by this
	// package.
	MetricsSubsystem = "consensus"
)

//go:generate go run ../../scripts/metricsgen -struct=Metrics

// Metrics contains metrics exposed by this package.
type Metrics struct {
	// Height of the chain.
	Height metrics.Gauge

	// Last height signed by this validator if the node is a validator.
	ValidatorLastSignedHeight metrics.Gauge `metrics_labels:"validator_address"`

	// Number of rounds.
	Rounds metrics.Gauge

	// Histogram of round duration.
	RoundDuration metrics.Histogram `metrics_buckettype:"exprange" metrics_bucketsizes:"0.1, 100, 8"`

	// Number of validators.
	Validators metrics.Gauge
	// Total power of all validators.
	ValidatorsPower metrics.Gauge
	// Power of a validator.
	ValidatorPower metrics.Gauge `metrics_labels:"validator_address"`
	// Amount of blocks missed per validator.
	ValidatorMissedBlocks metrics.Gauge `metrics_labels:"validator_address"`
	// Number of validators who did not sign.
	MissingValidators metrics.Gauge
	// Total power of the missing validators.
	MissingValidatorsPower metrics.Gauge
	// Number of validators who tried to double sign.
	ByzantineValidators metrics.Gauge
	// Total power of the byzantine validators.
	ByzantineValidatorsPower metrics.Gauge

	// Time between this and the last block.
	BlockIntervalSeconds metrics.Histogram

	// Number of transactions.
	NumTxs metrics.Gauge
	// Size of the block.
	BlockSizeBytes metrics.Histogram
	// Total number of transactions.
	TotalTxs metrics.Gauge
	// The latest block height.
	CommittedHeight metrics.Gauge `metrics_name:"latest_block_height"`
	// Whether or not a node is block syncing. 1 if yes, 0 if no.
	BlockSyncing metrics.Gauge
	// Whether or not a node is state syncing. 1 if yes, 0 if no.
	StateSyncing metrics.Gauge

	// Number of block parts transmitted by each peer.
	BlockParts metrics.Counter `metrics_labels:"peer_id"`

	// Histogram of durations for each step in the consensus protocol.
	StepDuration metrics.Histogram `metrics_labels:"step" metrics_buckettype:"exprange" metrics_bucketsizes:"0.1, 100, 8"`
	stepStart    time.Time

	// Histogram of time taken to receive a block in seconds, measured between when a new block is first
	// discovered to when the block is completed.
	BlockGossipReceiveLatency metrics.Histogram `metrics_buckettype:"exprange" metrics_bucketsizes:"0.1, 100, 8"`
	blockGossipStart          time.Time

	// Number of block parts received by the node, separated by whether the part
	// was relevant to the block the node is trying to gather or not.
	BlockGossipPartsReceived metrics.Counter `metrics_labels:"matches_current"`

	// QuroumPrevoteMessageDelay is the interval in seconds between the proposal
	// timestamp and the timestamp of the earliest prevote that achieved a quorum
	// during the prevote step.
	//
	// To compute it, sum the voting power over each prevote received, in increasing
	// order of timestamp. The timestamp of the first prevote to increase the sum to
	// be above 2/3 of the total voting power of the network defines the endpoint
	// the endpoint of the interval. Subtract the proposal timestamp from this endpoint
	// to obtain the quorum delay.
	//metrics:Interval in seconds between the proposal timestamp and the timestamp of the earliest prevote that achieved a quorum.
	QuorumPrevoteDelay metrics.Gauge `metrics_labels:"proposer_address"`

	// FullPrevoteDelay is the interval in seconds between the proposal
	// timestamp and the timestamp of the latest prevote in a round where 100%
	// of the voting power on the network issued prevotes.
	//metrics:Interval in seconds between the proposal timestamp and the timestamp of the latest prevote in a round where all validators voted.
	FullPrevoteDelay metrics.Gauge `metrics_labels:"proposer_address"`

	// ProposalTimestampDifference is the difference between the timestamp in
	// the proposal message and the local time of the validator at the time
	// that the validator received the message.
	//metrics:Difference between the timestamp in the proposal message and the local time of the validator at the time it received the message.
	ProposalTimestampDifference metrics.Histogram `metrics_labels:"is_timely" metrics_bucketsizes:"-10, -.5, -.025, 0, .1, .5, 1, 1.5, 2, 10"`

	// VoteExtensionReceiveCount is the number of vote extensions received by this
	// node. The metric is annotated by the status of the vote extension from the
	// application, either 'accepted' or 'rejected'.
	//metrics:Number of vote extensions received labeled by application response status.
	VoteExtensionReceiveCount metrics.Counter `metrics_labels:"status"`

	// VerificationBudgetDrops is the number of peer Vote and Commit messages
	// dropped because the signature-verification budget was exhausted.
	//metrics:Number of peer Vote and Commit messages dropped because the signature-verification budget was exhausted.
	VerificationBudgetDrops metrics.Counter

	// PeerLaneDrops is the number of queued peer messages dropped because the
	// sending peer had more waiting than the node buffers for it, or because the
	// peer disconnected before they were served. These are local shed decisions
	// and never count against the peer.
	//metrics:Number of queued peer consensus messages dropped by the per-peer scheduler.
	PeerLaneDrops metrics.Counter

	// BlockPartProofDrops is the number of peer block parts dropped without
	// verifying their proof, because the sending peer had already spent its
	// allowance on proofs that did not check out. A local shed decision that
	// never counts against the peer.
	//metrics:Number of peer block parts dropped before proof verification because the sender's invalid-proof allowance was spent.
	BlockPartProofDrops metrics.Counter

	// StateChannelDrops is the number of State- and VoteSetBits-channel messages
	// dropped because the sender, or the node as a whole, was over its ceiling.
	// A local shed decision that never counts against the peer.
	//metrics:Number of State and VoteSetBits channel messages dropped over the per-peer or node-wide ceiling.
	StateChannelDrops metrics.Counter

	// ProposalVerifyFailures is the number of peer proposals whose signature did
	// not verify. A flood makes this the only signal that they are arriving,
	// since the rejection itself is logged at debug.
	//metrics:Number of peer proposals whose signature failed verification.
	ProposalVerifyFailures metrics.Counter

	// ProposalReceiveCount is the total number of proposals received by this node
	// since process start.
	// The metric is annotated by the status of the proposal from the application,
	// either 'accepted' or 'rejected'.
	//metrics:Total number of proposals received by the node since process start labeled by application response status.
	ProposalReceiveCount metrics.Counter `metrics_labels:"status"`

	// ProposalCreationCount is the total number of proposals created by this node
	// since process start.
	//metrics:Total number of proposals created by the node since process start.
	ProposalCreateCount metrics.Counter

	// RoundVotingPowerPercent is the percentage of the total voting power received
	// with a round. The value begins at 0 for each round and approaches 1.0 as
	// additional voting power is observed. The metric is labeled by vote type.
	//metrics:A value between 0 and 1.0 representing the percentage of the total voting power per vote type received within a round.
	RoundVotingPowerPercent metrics.Gauge `metrics_labels:"vote_type"`

	// LateVotes stores the number of votes that were received by this node that
	// correspond to earlier heights and rounds than this node is currently
	// in.
	//metrics:Number of votes received by the node since process start that correspond to earlier heights and rounds than this node is currently in.
	LateVotes metrics.Counter `metrics_labels:"vote_type"`

	// PeerVoteVerifyLatencySeconds is the time an accepted peer vote spent
	// between the reactor queueing it and the vote set accepting it, covering the
	// wait in its per-peer lane, the wait for signature-verification budget, and
	// the signature check itself. It is the honest-service latency signal: when
	// the throttle is shedding attack traffic the votes it does accept are added
	// promptly, and when it is starving honest peers those same accepted votes
	// take longer. Only votes received from a peer are recorded; this node's own
	// votes and votes replayed from the write-ahead log are not.
	//metrics:Seconds an accepted peer vote spent from being queued by the reactor to being added to the vote set.
	PeerVoteVerifyLatencySeconds metrics.Histogram `metrics_bucketsizes:"0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5"`

	// VerificationBudgetSaturation is how full the node-wide signature
	// verification budget is when a peer message is checked against it: 1.0 when
	// the token bucket is untouched and the throttle is shedding nothing, falling
	// toward 0.0 as the budget drains and messages begin to wait or be dropped
	// for want of it. A value resting near 1.0 means the node is not
	// verification-bound; a value pinned near 0.0 means it is. It stays at 1.0
	// when signature-verification rate limiting is disabled.
	//metrics:Fraction of the node-wide verification budget still available when a peer message is checked, from 1.0 full and idle to 0.0 drained and under pressure.
	VerificationBudgetSaturation metrics.Gauge

	// PeerLaneActiveCount is how many per-peer lanes currently hold queued
	// messages and take turns in the scheduler rotation. It rises with the number
	// of peers sending faster than the node serves them, and an attacker cycling
	// through fresh node identities shows up here as lanes that keep appearing.
	//metrics:Number of per-peer lanes currently holding queued messages and taking turns in the scheduler rotation.
	PeerLaneActiveCount metrics.Gauge

	// PeerLaneMaxDepth is the largest number of messages queued in any single
	// peer lane, sampled as lanes are filled and served. A lane approaching its
	// capacity is a peer sending as fast as the node can accept, which is where
	// fairness pressure is felt first.
	//metrics:Largest number of messages queued in any single peer lane.
	PeerLaneMaxDepth metrics.Gauge
}

// RecordConsMetrics uses for recording the block related metrics during fast-sync.
//
// blockSize is the size of the serialized block in bytes. It is passed in rather
// than derived from block, because callers on the block sync path have already
// serialized the block and recomputing it there is expensive.
func (m *Metrics) RecordConsMetrics(block *types.Block, blockSize int64) {
	m.NumTxs.Set(float64(len(block.Data.Txs)))
	m.TotalTxs.Add(float64(len(block.Data.Txs)))
	m.BlockSizeBytes.Observe(float64(blockSize))
	m.CommittedHeight.Set(float64(block.Height))
}

func (m *Metrics) MarkBlockGossipStarted() {
	m.blockGossipStart = time.Now()
}

func (m *Metrics) MarkBlockGossipComplete() {
	m.BlockGossipReceiveLatency.Observe(time.Since(m.blockGossipStart).Seconds())
}

func (m *Metrics) MarkProposalProcessed(accepted bool) {
	status := "accepted"
	if !accepted {
		status = "rejected"
	}
	m.ProposalReceiveCount.With("status", status).Add(1)
}

func (m *Metrics) MarkVoteExtensionReceived(accepted bool) {
	status := "accepted"
	if !accepted {
		status = "rejected"
	}
	m.VoteExtensionReceiveCount.With("status", status).Add(1)
}

func (m *Metrics) MarkVoteReceived(vt tmproto.SignedMsgType, power, totalPower int64) {
	p := float64(power) / float64(totalPower)
	n := strings.ToLower(strings.TrimPrefix(vt.String(), "SIGNED_MSG_TYPE_"))
	m.RoundVotingPowerPercent.With("vote_type", n).Add(p)
}

func (m *Metrics) MarkRound(r int32, st time.Time) {
	m.Rounds.Set(float64(r))
	roundTime := time.Since(st).Seconds()
	m.RoundDuration.Observe(roundTime)

	pvt := tmproto.PrevoteType
	pvn := strings.ToLower(strings.TrimPrefix(pvt.String(), "SIGNED_MSG_TYPE_"))
	m.RoundVotingPowerPercent.With("vote_type", pvn).Set(0)

	pct := tmproto.PrecommitType
	pcn := strings.ToLower(strings.TrimPrefix(pct.String(), "SIGNED_MSG_TYPE_"))
	m.RoundVotingPowerPercent.With("vote_type", pcn).Set(0)
}

func (m *Metrics) MarkLateVote(vt tmproto.SignedMsgType) {
	n := strings.ToLower(strings.TrimPrefix(vt.String(), "SIGNED_MSG_TYPE_"))
	m.LateVotes.With("vote_type", n).Add(1)
}

func (m *Metrics) MarkStep(s cstypes.RoundStepType) {
	if !m.stepStart.IsZero() {
		stepTime := time.Since(m.stepStart).Seconds()
		stepName := strings.TrimPrefix(s.String(), "RoundStep")
		m.StepDuration.With("step", stepName).Observe(stepTime)
	}
	m.stepStart = time.Now()
}
