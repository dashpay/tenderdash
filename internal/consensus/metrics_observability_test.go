package consensus

import (
	"context"
	"testing"
	"time"

	"github.com/go-kit/kit/metrics"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// recordingHistogram keeps every value observed, so a test can assert a
// histogram both received a sample and by how much it moved.
type recordingHistogram struct {
	values []float64
}

func (h *recordingHistogram) With(...string) metrics.Histogram { return h }

func (h *recordingHistogram) Observe(value float64) { h.values = append(h.values, value) }

// recordingGauge keeps the last value set and whether it was ever set, so a
// test can tell a sampled gauge from an untouched one.
type recordingGauge struct {
	value float64
	set   bool
}

func (g *recordingGauge) With(...string) metrics.Gauge { return g }

func (g *recordingGauge) Set(value float64) {
	g.value = value
	g.set = true
}

func (g *recordingGauge) Add(delta float64) {
	g.value += delta
	g.set = true
}

// prevoteMsg is the cheapest priceable peer message: a prevote costs one
// signature verification and needs no state to build.
func prevoteMsg(peerID types.NodeID) msgInfo {
	return msgInfo{
		Msg:    &VoteMessage{Vote: &types.Vote{Type: tmproto.PrevoteType}},
		PeerID: peerID,
	}
}

// TestAddVoteActionRecordsPeerVoteVerifyLatency pins the honest-service latency
// signal: an accepted peer vote records the time from when the reactor queued
// it to now, and nothing else does. A vote that was not added, one of this
// node's own votes, and one replayed from the write-ahead log must all leave the
// histogram untouched, so the metric measures peer service and not replay or
// self-votes whose queue timestamps are meaningless here.
func TestAddVoteActionRecordsPeerVoteVerifyLatency(t *testing.T) {
	testCases := []struct {
		name        string
		peerID      types.NodeID
		fromReplay  bool
		added       bool
		wantSamples int
	}{
		{name: "accepted peer vote", peerID: "peer", added: true, wantSamples: 1},
		{name: "rejected peer vote", peerID: "peer", added: false, wantSamples: 0},
		{name: "accepted local vote", peerID: "", added: true, wantSamples: 0},
		{name: "accepted replayed vote", peerID: "peer", fromReplay: true, added: true, wantSamples: 0},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hist := &recordingHistogram{}
			m := NopMetrics()
			m.PeerVoteVerifyLatencySeconds = hist
			action := AddVoteAction{
				metrics: m,
				prevote: func(context.Context, *StateData, *types.Vote) (bool, error) {
					return tc.added, nil
				},
			}
			// The message was queued in the past, so an accepted vote must record
			// a strictly positive latency.
			ctx := msgInfoWithCtx(context.Background(), msgInfo{
				PeerID:      tc.peerID,
				ReceiveTime: time.Now().Add(-50 * time.Millisecond),
			})
			err := action.Execute(ctx, StateEvent{
				Data: &AddVoteEvent{
					Vote:       &types.Vote{Type: tmproto.PrevoteType},
					PeerID:     tc.peerID,
					FromReplay: tc.fromReplay,
				},
			})
			require.NoError(t, err)
			require.Len(t, hist.values, tc.wantSamples)
			if tc.wantSamples > 0 {
				require.Greater(t, hist.values[0], 0.0,
					"a peer vote queued in the past and then accepted must record a positive latency")
			}
		})
	}
}

// TestVerificationBudgetSaturation pins the direction of the saturation signal:
// full when the budget is idle, near empty when it is drained, and full when the
// budget does not meter at all.
func TestVerificationBudgetSaturation(t *testing.T) {
	t.Run("idle budget reports full", func(t *testing.T) {
		budget := newVerificationBudget(verificationRate)
		require.Equal(t, 1.0, budget.saturation())
	})

	t.Run("drained budget reports near empty", func(t *testing.T) {
		clock := clockwork.NewFakeClock()
		budget := newVerificationBudget(verificationRate, withVerificationBudgetClock(clock))
		drainVerificationBudget(budget)
		require.Less(t, budget.saturation(), 0.01,
			"a bucket spent down to nothing must read near zero")
	})

	t.Run("disabled budget reports full", func(t *testing.T) {
		budget := newVerificationBudget(0)
		require.Equal(t, 1.0, budget.saturation())
	})
}

// TestPeerLaneAffordableSamplesBudgetSaturation shows the scheduler samples the
// saturation gauge when it checks a peer message against the budget, and that
// the sample follows the budget from full down to near empty.
func TestPeerLaneAffordableSamplesBudgetSaturation(t *testing.T) {
	clock := clockwork.NewFakeClock()
	budget := newVerificationBudget(verificationRate, withVerificationBudgetClock(clock))
	gauge := &recordingGauge{}
	m := NopMetrics()
	m.VerificationBudgetSaturation = gauge
	lanes := newPeerLanes(withLaneBudget(budget), withLaneMetrics(m), withLaneClock(clock))

	// A full budget covers the message at once, and the gauge is sampled full.
	require.True(t, lanes.affordable(context.Background(), prevoteMsg("peer")))
	require.True(t, gauge.set, "affordable must sample the saturation gauge")
	require.Equal(t, 1.0, gauge.value)

	// Drain the budget, then check a zero-cost message so the scheduler admits it
	// without waiting on the frozen clock: the gauge is resampled near empty.
	drainVerificationBudget(budget)
	require.True(t, lanes.affordable(context.Background(),
		msgInfo{Msg: &VoteMessage{Vote: nil}, PeerID: "peer"}))
	require.Less(t, gauge.value, 0.01, "a drained budget must sample near empty")
}

// TestPeerLaneDepthGauges shows the lane-depth gauges track the rotation as
// lanes are filled and served: the active count follows how many peers hold a
// lane, and the max depth follows the deepest lane.
func TestPeerLaneDepthGauges(t *testing.T) {
	activeGauge := &recordingGauge{}
	depthGauge := &recordingGauge{}
	m := NopMetrics()
	m.PeerLaneActiveCount = activeGauge
	m.PeerLaneMaxDepth = depthGauge
	lanes := newPeerLanes(withLaneMetrics(m))

	// Peer A queues three messages, peer B one.
	ctx := context.Background()
	lanes.enqueue(ctx, prevoteMsg("A"))
	lanes.enqueue(ctx, prevoteMsg("A"))
	lanes.enqueue(ctx, prevoteMsg("A"))
	lanes.enqueue(ctx, prevoteMsg("B"))
	require.Equal(t, 2.0, activeGauge.value, "two peers hold a lane")
	require.Equal(t, 3.0, depthGauge.value, "peer A's lane is the deepest at three")

	// The rotation serves peer A's head first; its lane drops to two but stays
	// the deepest, and both lanes remain active.
	_, ok := lanes.next()
	require.True(t, ok)
	require.Equal(t, 2.0, activeGauge.value)
	require.Equal(t, 2.0, depthGauge.value)
}
