package consensus

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/types"
)

// tokenBudget is a verification budget holding a fixed number of tokens that
// never refill, so a test decides exactly which verifications it can afford.
type tokenBudget struct {
	tokens  int
	charged int
}

func (b *tokenBudget) Allow(cost int) bool {
	if cost > b.tokens {
		return false
	}
	b.tokens -= cost
	b.charged += cost
	return true
}

func (f *rejectRecoveryFixture) setBudget(budget types.VerificationBudget) {
	f.node.ctrl.Get(TryAddCommitType).(*TryAddCommitAction).verificationBudget = budget
}

// A peer resending an authentic commit with stripped extensions must not make
// this node change rounds: every round retains vote sets and lengthens the
// timeouts, so a rejection that advanced the round would let a single peer grow
// both without bound.
func TestRepeatedRejectedCommitKeepsRound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newRejectRecoveryFixture(ctx, t)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
	round, voteRound := f.stateData.Round, f.stateData.Votes.Round()

	f.sendCommit(ctx, t, f.bad, "attacker")
	require.NoError(t, f.completeBlock(ctx))
	const resends = 50
	for range resends {
		f.sendCommit(ctx, t, f.bad, "attacker")
	}

	require.Equal(t, f.block.Height, f.stateData.Height)
	require.Equal(t, round, f.stateData.Round, "a rejected commit must not advance the round")
	require.Equal(t, voteRound, f.stateData.Votes.Round(), "a rejected commit must not add vote sets")
	require.Nil(t, f.stateData.Votes.Prevotes(round+1))
	require.Equal(t, cstypes.RoundStepPropose, f.stateData.Step, "the round keeps its step and its timeout")

	f.sendCommit(ctx, t, f.good, "honest")
	f.requireCommitted(t, f.good)
	require.Len(t, f.checker.calls, 1+(1+resends)+1+1)
}

// More peers than any fixed cap queue a commit while one is parked; an honest
// commit behind all of them must still finish the height.
func TestCommitQueueHasSlotForEveryPeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newRejectRecoveryFixture(ctx, t)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)

	const sybils = 40
	for i := range sybils {
		f.sendCommit(ctx, t, f.bad, types.NodeID(fmt.Sprintf("sybil-%d", i)))
	}
	f.sendCommit(ctx, t, f.good, "honest")
	require.NoError(t, f.completeBlock(ctx))

	f.requireCommitted(t, f.good)
	require.Len(t, f.checker.calls, 1+sybils+1+1)
}

// Queued commits pay for their verification when they arrive, in their own
// scheduler turn. Retrying them after a rejection must not charge the budget
// again: that charge would be out of turn, when the budget may hold nothing.
func TestCommitRecoveryDoesNotChargeBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newRejectRecoveryFixture(ctx, t)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
	budget := &tokenBudget{tokens: 1_000}
	f.setBudget(budget)

	f.sendCommit(ctx, t, f.bad, "attacker-1")
	f.sendCommit(ctx, t, f.bad, "attacker-2")
	f.sendCommit(ctx, t, f.bad, "attacker-3")
	f.sendCommit(ctx, t, f.good, "honest")
	prepaid, err := commitCost(len(f.good.ThresholdVoteExtensions))
	require.NoError(t, err)
	require.Equal(t, 1+1+1+prepaid, budget.charged, "every commit pays in its own turn")

	budget.tokens = 0
	require.NoError(t, f.completeBlock(ctx))
	f.requireCommitted(t, f.good)
}

// A commit this node could not afford to verify is kept in its sender's slot:
// the sender will not send it again, so dropping it would lose it.
func TestBudgetExhaustedCommitIsRetained(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newRejectRecoveryFixture(ctx, t)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
	budget := &tokenBudget{}
	f.setBudget(budget)

	commitCtx := msgInfoWithCtx(ctx, msgInfo{Msg: &CommitMessage{f.good}, PeerID: "honest"})
	f.node.msgInfoQueue.admitPeer("honest")
	err := f.node.ctrl.Dispatch(commitCtx, &TryAddCommitEvent{Commit: f.good, PeerID: "honest"}, &f.stateData)
	require.ErrorIs(t, err, types.ErrVerificationBudgetExhausted)
	require.Nil(t, f.stateData.Commit)

	budget.tokens = 1_000
	f.sendCommit(ctx, t, f.bad, "attacker")
	require.NoError(t, f.completeBlock(ctx))
	f.requireCommitted(t, f.good)
}

// A validator whose own quorum's commit is rejected returns to Precommit, the
// step its quorum passed through, so a pending PrecommitWait timeout moves the
// round on at its ordinary pace, and resent rejected commits do not.
func TestOwnQuorumRejectionKeepsTimeoutPace(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newRejectRecoveryFixture(ctx, t)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
	f.checker.rejectAll = true

	f.deliver(ctx, t, &f.stateData, f.precommit(ctx, t, f.commit.BlockID))
	require.Equal(t, cstypes.RoundStepApplyCommit, f.stateData.Step)
	require.NoError(t, f.completeBlock(ctx))

	require.Equal(t, int32(0), f.stateData.Round)
	require.Equal(t, cstypes.RoundStepPrecommit, f.stateData.Step)
	for range 50 {
		f.sendCommit(ctx, t, f.bad, "attacker")
	}
	require.Equal(t, int32(0), f.stateData.Round, "resent commits must not advance the round")
	require.Equal(t, cstypes.RoundStepPrecommit, f.stateData.Step)

	f.node.handleTimeout(ctx, timeoutInfo{Height: f.block.Height, Round: 0, Step: cstypes.RoundStepPrecommitWait}, &f.stateData)
	require.Equal(t, int32(1), f.stateData.Round, "the pending timeout still moves the round on")
}
