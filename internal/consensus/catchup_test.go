package consensus

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

// TestCatchupTrackerMayPropose covers the whole suppression window: a node that
// block sync handed to consensus while it was provably behind must not propose
// until something says it reached the network - a peer reporting a height no
// higher than its own, or a block committed through consensus while no peer
// reports one. A peer that has reported nothing yet is not that something.
func TestCatchupTrackerMayPropose(t *testing.T) {
	const ourHeight = int64(100)
	testCases := []struct {
		name       string
		armed      bool
		peerHeight int64
		committed  bool
		want       bool
	}{
		{
			name: "handover was caught up",
			want: true,
		},
		{
			name:       "behind, nothing committed through consensus yet",
			armed:      true,
			peerHeight: ourHeight + 5000,
			want:       false,
		},
		{
			name:  "no peer has reported a height yet",
			armed: true,
			want:  false,
		},
		{
			name:       "committed, but a peer is still ahead",
			armed:      true,
			peerHeight: ourHeight + 1,
			committed:  true,
			want:       false,
		},
		{
			name:       "committed, no peer above us",
			armed:      true,
			peerHeight: ourHeight,
			committed:  true,
			want:       true,
		},
		{
			name:      "no peer has reported a height, but a block was committed",
			armed:     true,
			committed: true,
			want:      true,
		},
		{
			name:       "a peer reports our own height, nothing committed yet",
			armed:      true,
			peerHeight: ourHeight,
			want:       true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tracker := &catchupTracker{}
			if tc.armed {
				tracker.arm(func() int64 { return tc.peerHeight })
			}
			if tc.committed {
				tracker.blockCommitted()
			}
			require.Equal(t, tc.want, tracker.mayPropose(ourHeight))
		})
	}
}

// TestCatchupTrackerDoesNotRearm is what keeps the suppression out of reach of a
// peer that lies about its height. Only a block-sync handover can start it - a
// state the node enters only while provably behind - so once the node has caught
// up, a peer claiming to be far ahead cannot silence it again.
func TestCatchupTrackerDoesNotRearm(t *testing.T) {
	const ourHeight = int64(100)
	peerHeight := ourHeight

	tracker := &catchupTracker{}
	tracker.arm(func() int64 { return peerHeight })
	tracker.blockCommitted()
	require.True(t, tracker.mayPropose(ourHeight))

	peerHeight = ourHeight + 1_000_000
	require.True(t, tracker.mayPropose(ourHeight), "a peer's claimed height must not suppress proposing again")
}

// TestCatchupTrackerNotWired checks that a tracker nobody armed permits
// proposing, so a state built without one proposes as it always did.
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
	cs.catchup.arm(func() int64 { return height + 5000 })

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
	cs.catchup.arm(func() int64 { return height })
	cs.catchup.blockCommitted()

	stateData = cs.GetStateData()
	require.NoError(t, cs.ctrl.Dispatch(ctx, &EnterProposeEvent{Height: height, Round: round}, &stateData))
	require.Equal(t, int64(1), creator.calls.Load(), "a caught up proposer built nothing")
}
