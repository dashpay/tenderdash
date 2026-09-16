package consensus

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

func TestCatchupTrackerMayPropose(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target int64
		height int64
		want   bool
	}{
		{"not armed", 0, 100, true},
		{"historical height", 5000, 100, false},
		{"target block not yet applied", 100, 100, false},
		{"target block applied", 100, 101, true},
		{"beyond target", 100, 102, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tracker := &catchupTracker{}
			if tc.target > 0 {
				tracker.arm(tc.target, clockwork.NewFakeClock())
			}
			require.Equal(t, tc.want, tracker.mayPropose(tc.height))
		})
	}
}

func TestCatchupTrackerExpiry(t *testing.T) {
	clock := clockwork.NewFakeClock()
	tracker := &catchupTracker{}
	tracker.arm(5000, clock)
	require.False(t, tracker.mayPropose(100))
	clock.Advance(maxCatchupSuppression - time.Nanosecond)
	require.False(t, tracker.mayPropose(101), "partial local progress must preserve the target")
	clock.Advance(time.Nanosecond)
	require.True(t, tracker.mayPropose(102), "partial progress must not extend an unverified claim")
	require.True(t, tracker.mayPropose(102), "expiry must permanently release the gate")
}

func TestCatchupTrackerDoesNotRearm(t *testing.T) {
	clock := clockwork.NewFakeClock()
	tracker := &catchupTracker{}
	tracker.arm(100, clock)
	require.True(t, tracker.mayPropose(101))
	require.True(t, tracker.mayPropose(100), "only a new handover may re-arm the tracker")
	tracker.arm(102, clock)
	require.False(t, tracker.mayPropose(101), "a new handover must retain its own target")
}

func TestCatchupTrackerNotWired(t *testing.T) {
	var tracker *catchupTracker
	require.True(t, tracker.mayPropose(1))
}

// countingProposalCreator records how many proposals the propose step asked for.
type countingProposalCreator struct {
	calls atomic.Int64
}

func (c *countingProposalCreator) Create(_ context.Context, _ int64, _ int32, _ *cstypes.RoundState) error {
	c.calls.Add(1)
	return nil
}

// enterProposeWithCountingCreator replaces the propose step's proposal creator
// with a counter and returns it.
func enterProposeWithCountingCreator(cs *State) *countingProposalCreator {
	creator := &countingProposalCreator{}
	cs.ctrl.Get(EnterProposeType).(*EnterProposeAction).proposalCreator = creator
	return creator
}

// TestEnterProposeSuppressedWhileCatchingUp checks that the proposer of a height
// the network committed long ago builds nothing. The block it would build comes
// from present-day application state, and the genuine block for that height
// collides with it as soon as the network's commit arrives
// (dashpay/tenderdash#1413).
func TestEnterProposeSuppressedWhileCatchingUp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cs, _ := makeState(ctx, t, makeStateArgs{validators: 1, logger: log.NewNopLogger()})
	cs.config.DontAutoPropose = true
	ctx = dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash)
	stateData := cs.GetStateData()
	height, round := stateData.Height, stateData.Round

	newRoundCh := subscribe(ctx, t, cs.eventBus, types.EventQueryNewRound)
	startTestRound(ctx, cs, height, round)
	ensureNewRound(t, newRoundCh, height, round)

	creator := enterProposeWithCountingCreator(cs)
	cs.catchup.arm(height+5000, clockwork.NewFakeClock())

	stateData = cs.GetStateData()
	require.NoError(t, cs.ctrl.Dispatch(ctx, &EnterProposeEvent{Height: height, Round: round}, &stateData))
	require.Zero(t, creator.calls.Load(), "a node still catching up proposed a block")
}

// TestEnterProposeResumesOnceCaughtUp is the regression guard on the test above:
// the suppression must end, or a bad handover would cost the network a proposer
// until the node is restarted.
func TestEnterProposeResumesOnceCaughtUp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cs, _ := makeState(ctx, t, makeStateArgs{validators: 1, logger: log.NewNopLogger()})
	cs.config.DontAutoPropose = true
	ctx = dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash)
	stateData := cs.GetStateData()
	height, round := stateData.Height, stateData.Round

	newRoundCh := subscribe(ctx, t, cs.eventBus, types.EventQueryNewRound)
	startTestRound(ctx, cs, height, round)
	ensureNewRound(t, newRoundCh, height, round)

	creator := enterProposeWithCountingCreator(cs)
	cs.catchup.arm(height-1, clockwork.NewFakeClock())

	stateData = cs.GetStateData()
	require.NoError(t, cs.ctrl.Dispatch(ctx, &EnterProposeEvent{Height: height, Round: round}, &stateData))
	require.Equal(t, int64(1), creator.calls.Load(), "a caught up proposer built nothing")
}
