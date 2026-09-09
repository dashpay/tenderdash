package consensus

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/dash"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// A precommit is verified once, and that pass charges the budget in two stages.
// Waiting for the whole message up front is what keeps the second stage from
// being denied after the first already paid for a signature check — which would
// throw away both the work and a valid vote.
//
// The exact charges are asserted because the admitted cost only covers them by
// virtue of how the verification path happens to be built: a second pass, or a
// newly budgeted step, would silently break that.
func TestVerificationBudgetWaitCompletesStagedPrecommitDraws(t *testing.T) {
	const extensions = 4

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	clock := clockwork.NewFakeClock()
	inner := newVerificationBudget(300, withVerificationBudgetClock(clock))
	budget := &recordingVerificationBudget{inner: inner}

	cs, vss := makeState(ctx, t, makeStateArgs{
		validators: 4,
		stateOpts:  []StateOption{WithVerificationBudget(budget)},
	})
	stateData := cs.GetStateData()

	// Leave the bucket able to pay the first staged charge but not the vote
	// extensions that follow it: without the wait the message would be denied
	// half-way through verification.
	drainVerificationBudget(inner)
	clock.Advance(10 * time.Millisecond)
	require.Less(t, inner.limiter.TokensAt(clock.Now()), float64(extensions))

	stopAdvancing := advanceClockWhileWaiting(ctx, clock)
	defer stopAdvancing()

	vote := signPrecommitWithExtensions(ctx, t, vss[1], &stateData, extensions)
	mi := msgInfo{Msg: &VoteMessage{Vote: vote}, PeerID: "peer", ReceiveTime: tmtime.Now()}

	require.NoError(t, cs.msgDispatcher.dispatch(dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash), &stateData, mi))

	require.Equal(t, []int{1 + extensions}, budget.waitedFor(),
		"the whole message must be made affordable before it is verified")
	require.Equal(t, []int{1, extensions}, budget.charges(),
		"verification charges the block signature, then the vote extensions")
	require.Equal(t, []bool{true, true}, budget.outcomes(),
		"no staged charge may be denied once the message has been admitted")
	require.NotNil(t,
		stateData.Votes.GetVoteSet(vote.Round, tmproto.PrecommitType).GetByIndex(vote.ValidatorIndex),
		"the vote must reach the vote set instead of being dropped mid-verification")
}

// Shedding must be free. The check runs outside the WAL middleware, so a
// dropped message costs no disk write — and, on replay, no re-verification of
// it under a disabled budget.
func TestVerificationBudgetShedCostsNoWALWriteAndNoPeerError(t *testing.T) {
	const extensions = 4

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	clock := clockwork.NewFakeClock()
	inner := newVerificationBudget(300, withVerificationBudgetClock(clock))
	budget := &recordingVerificationBudget{inner: inner}

	cs, vss := makeState(ctx, t, makeStateArgs{
		validators: 4,
		stateOpts:  []StateOption{WithVerificationBudget(budget)},
	})
	stateData := cs.GetStateData()
	wal := &countingWAL{}
	cs.wal = wal

	vote := signPrecommitWithExtensions(ctx, t, vss[1], &stateData, extensions)
	mi := msgInfo{Msg: &VoteMessage{Vote: vote}, PeerID: "peer", ReceiveTime: tmtime.Now()}
	ctx = dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash)

	// Give up immediately rather than waiting out the refill.
	inner.maxWait = 0
	drainVerificationBudget(inner)

	require.NoError(t, cs.msgDispatcher.dispatch(ctx, &stateData, mi))

	require.Zero(t, wal.writes, "a shed message must not reach the write-ahead log")
	require.Empty(t, budget.charges(), "a shed message must not be verified")
	require.Empty(t, cs.peerErrorQueue.ch, "local overload is not the sender's fault")
	require.Nil(t,
		stateData.Votes.GetVoteSet(vote.Round, tmproto.PrecommitType).GetByIndex(vote.ValidatorIndex))

	// The same message written to the WAL once it is affordable proves the
	// check really does run ahead of the WAL, rather than the WAL being
	// unreachable in this test.
	clock.Advance(time.Second)
	require.NoError(t, cs.msgDispatcher.dispatch(ctx, &stateData, mi))
	require.Equal(t, 1, wal.writes)
}

// Every charge is made by the single goroutine that dispatches messages. The
// whole-message wait is a reservation only because of that: another consumer
// could take the tokens between the wait and the charges.
func TestVerificationBudgetChargedOnlyOnConsensusGoroutine(t *testing.T) {
	const extensions = 4

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	budget := &recordingVerificationBudget{inner: newVerificationBudget(300)}
	cs, vss := makeState(ctx, t, makeStateArgs{
		validators: 4,
		stateOpts:  []StateOption{WithVerificationBudget(budget)},
	})
	stateData := cs.GetStateData()
	vote := signPrecommitWithExtensions(ctx, t, vss[1], &stateData, extensions)

	ctx = dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash)
	go cs.receiveRoutine(ctx, nil)

	require.NoError(t, cs.msgInfoQueue.send(ctx, &VoteMessage{Vote: vote}, "peer"))

	require.Eventually(t, func() bool {
		return len(budget.charges()) == 2
	}, 10*time.Second, 10*time.Millisecond, "the peer precommit was never verified")
	require.Len(t, budget.goroutines(), 1, "the budget was charged from more than one goroutine")
	require.NotEqual(t, goroutineID(), budget.goroutines()[0],
		"charges must be made by the consensus goroutine, not by whoever submits the message")
}

// A wait that ignored cancellation would hold the consensus goroutine open
// forever: the queue reader's shutdown path waits for it to return.
func TestVerificationBudgetWaitDoesNotBlockShutdown(t *testing.T) {
	const extensions = 4

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clock := clockwork.NewFakeClock()
	inner := newVerificationBudget(300, withVerificationBudgetClock(clock))
	budget := &recordingVerificationBudget{inner: inner}
	cs, vss := makeState(ctx, t, makeStateArgs{
		validators: 4,
		stateOpts:  []StateOption{WithVerificationBudget(budget)},
	})
	stateData := cs.GetStateData()
	vote := signPrecommitWithExtensions(ctx, t, vss[1], &stateData, extensions)
	drainVerificationBudget(inner)

	routineCtx, stopRoutine := context.WithCancel(dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash))
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		cs.receiveRoutine(routineCtx, nil)
	}()

	require.NoError(t, cs.msgInfoQueue.send(routineCtx, &VoteMessage{Vote: vote}, "peer"))
	require.NoError(t, clock.BlockUntilContext(ctx, 1), "the message must be waiting for budget")

	stopRoutine()
	select {
	case <-returned:
	case <-time.After(10 * time.Second):
		t.Fatal("consensus goroutine did not return while waiting for verification budget")
	}
}

// advanceClockWhileWaiting keeps a fake clock moving whenever something waits
// on it, so that a bounded wait for budget completes without the test having to
// know how long it asked for.
func advanceClockWhileWaiting(ctx context.Context, clock *clockwork.FakeClock) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if err := clock.BlockUntilContext(ctx, 1); err != nil {
				return
			}
			clock.Advance(50 * time.Millisecond)
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func signPrecommitWithExtensions(
	ctx context.Context,
	t *testing.T,
	vs *validatorStub,
	stateData *StateData,
	extensions int,
) *types.Vote {
	t.Helper()
	exts := testPrecommitVote(extensions).VoteExtensions
	vote, err := vs.signVote(
		ctx,
		tmproto.PrecommitType,
		stateData.state.ChainID,
		factory.MakeBlockID(),
		stateData.Validators.QuorumType,
		stateData.Validators.QuorumHash,
		exts,
	)
	require.NoError(t, err)
	return vote
}

// countingWAL counts write-ahead log records without writing anything.
type countingWAL struct {
	nilWAL
	writes int
}

func (w *countingWAL) Write(WALMessage) error {
	w.writes++
	return nil
}

func (w *countingWAL) WriteSync(WALMessage) error {
	w.writes++
	return nil
}
