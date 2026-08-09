package consensus

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/dash"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	"github.com/dashpay/tenderdash/types"
)

// A proposal forces a BLS pairing on the consensus goroutine, so it has to be
// priced like every other message that does.
func TestProposalIsPricedForTheVerificationBudget(t *testing.T) {
	cost, err := budgetedMessageCost(&ProposalMessage{Proposal: &types.Proposal{}})
	require.NoError(t, err)
	require.Equal(t, baseMessageCost, cost,
		"a proposal verifies one signature and must be charged for it")
}

// The proposal path has no de-duplication that survives a bad signature:
// rs.Proposal is only set by a proposal that verifies, so every forged copy is
// verified again. The budget is therefore the only thing standing between a
// proposal flood and the verifier, and it has to be drawn on where the pairing
// happens.
func TestProposalVerificationDrawsOnTheVerificationBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	clock := clockwork.NewFakeClock()
	inner := newVerificationBudget(300, withVerificationBudgetClock(clock))
	budget := &recordingVerificationBudget{inner: inner}

	cs, _ := makeState(ctx, t, makeStateArgs{
		validators: 4,
		stateOpts:  []StateOption{WithVerificationBudget(budget)},
	})
	stateData := cs.GetStateData()
	ctx = dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash)

	mi := msgInfo{
		Msg:         &ProposalMessage{Proposal: forgedProposal(&stateData)},
		PeerID:      "peer",
		ReceiveTime: tmtime.Now(),
	}
	require.NoError(t, cs.msgDispatcher.dispatch(ctx, &stateData, mi))

	require.Equal(t, []int{baseMessageCost}, budget.waitedFor(),
		"the proposal must be made affordable before it is verified")
	require.Equal(t, []int{baseMessageCost}, budget.charges(),
		"verifying a proposal signature must be charged to the budget")
	require.Nil(t, stateData.Proposal, "a forged proposal must not be accepted")
}

// The flood this closes: a forged proposal is never de-duplicated, so without a
// permit every copy reaches the verifier. With one, an exhausted budget stops
// them before the pairing.
func TestProposalFloodCannotExceedTheVerificationBudget(t *testing.T) {
	const flood = 50

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	clock := clockwork.NewFakeClock()
	inner := newVerificationBudget(300, withVerificationBudgetClock(clock))
	budget := &recordingVerificationBudget{inner: inner}

	cs, _ := makeState(ctx, t, makeStateArgs{
		validators: 4,
		stateOpts:  []StateOption{WithVerificationBudget(budget)},
	})
	stateData := cs.GetStateData()
	ctx = dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash)

	wal := &countingWAL{}
	cs.wal = wal

	// Give up immediately rather than waiting out the refill, so the test
	// measures admission and not the length of the bounded wait.
	inner.maxWait = 0
	drainVerificationBudget(inner)

	for i := 0; i < flood; i++ {
		mi := msgInfo{
			Msg:         &ProposalMessage{Proposal: forgedProposal(&stateData)},
			PeerID:      types.NodeID("peer"),
			ReceiveTime: tmtime.Now(),
		}
		require.NoError(t, cs.msgDispatcher.dispatch(ctx, &stateData, mi))
	}

	require.Empty(t, budget.charges(),
		"an exhausted budget must stop a proposal flood before the signature check")
	require.Zero(t, wal.writes, "a shed proposal must not reach the write-ahead log")
	require.Empty(t, cs.peerErrorQueue.ch, "local overload is not the sender's fault")

	// The same proposal admitted once the bucket has refilled proves the flood
	// was stopped by the budget and not by something else on the path.
	clock.Advance(time.Second)
	mi := msgInfo{
		Msg:         &ProposalMessage{Proposal: forgedProposal(&stateData)},
		PeerID:      types.NodeID("peer"),
		ReceiveTime: tmtime.Now(),
	}
	require.NoError(t, cs.msgDispatcher.dispatch(ctx, &stateData, mi))
	require.Equal(t, []int{baseMessageCost}, budget.charges())
}

// This node's own proposals, and those replayed from the write-ahead log, are
// not what the budget bounds; charging them would let a saturated budget stall
// our own progress.
func TestLocalAndReplayedProposalsAreNotCharged(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	clock := clockwork.NewFakeClock()
	inner := newVerificationBudget(300, withVerificationBudgetClock(clock))
	budget := &recordingVerificationBudget{inner: inner}

	cs, _ := makeState(ctx, t, makeStateArgs{
		validators: 4,
		stateOpts:  []StateOption{WithVerificationBudget(budget)},
	})
	stateData := cs.GetStateData()
	ctx = dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash)

	inner.maxWait = 0
	drainVerificationBudget(inner)

	local := msgInfo{Msg: &ProposalMessage{Proposal: forgedProposal(&stateData)}, ReceiveTime: tmtime.Now()}
	require.NoError(t, cs.msgDispatcher.dispatch(ctx, &stateData, local))

	replayed := msgInfo{
		Msg:         &ProposalMessage{Proposal: forgedProposal(&stateData)},
		PeerID:      "peer",
		ReceiveTime: tmtime.Now(),
	}
	require.NoError(t, cs.msgDispatcher.dispatch(ctx, &stateData, replayed, msgFromReplay()))

	require.Empty(t, budget.charges(), "neither local nor replayed proposals draw on the budget")
}

// forgedProposal builds a proposal that is structurally acceptable for the
// current height and round but carries a signature nobody made, so it reaches
// the signature check and fails it — leaving rs.Proposal unset, which is what
// makes the flood self-sustaining.
func forgedProposal(stateData *StateData) *types.Proposal {
	proposal := types.NewProposal(
		stateData.Height,
		stateData.state.LastCoreChainLockedBlockHeight,
		stateData.Round,
		-1,
		factory.MakeBlockID(),
		tmtime.Now(),
	)
	proposal.Signature = crypto.CRandBytes(types.SignatureSize)
	return proposal
}

// Pricing the proposal is only half the fix: the peer scheduler must gate on
// the same price, otherwise a flood is still handed to the consensus goroutine
// at whatever rate it arrives and the budget only refuses it afterwards -- after
// the write-ahead log record and the turn have already been spent.
func TestPeerSchedulerHoldsProposalsForTheVerificationBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	clock := clockwork.NewFakeClock()
	budget := newVerificationBudget(300, withVerificationBudgetClock(clock))
	drainVerificationBudget(budget)
	lanes := newPeerLanes(withLaneBudget(budget), withLaneClock(clock))

	proposal := msgInfo{
		Msg:    &ProposalMessage{Proposal: &types.Proposal{}},
		PeerID: "peer",
	}
	require.NoError(t, lanes.send(ctx, proposal))

	// The clock never advances on its own, so a scheduler that gates on the
	// proposal's price has to be waiting on it.
	handed := make(chan bool, 1)
	go func() {
		_, ok := lanes.recv(ctx)
		handed <- ok
	}()

	require.NoError(t, clock.BlockUntilContext(ctx, 1),
		"the scheduler handed over a proposal without making room for its verification")

	// Once the bucket has refilled it goes through, so the wait was the budget
	// and not something else.
	clock.Advance(time.Second)
	select {
	case ok := <-handed:
		require.True(t, ok)
	case <-time.After(5 * time.Second):
		t.Fatal("the proposal was never handed over after the budget refilled")
	}
}
