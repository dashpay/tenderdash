package consensus

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// The bucket must hold more than the most expensive single message, not exactly
// as much: at equality a maximum-cost message is admissible only while the
// bucket is completely full, which on a busy node never happens.
func TestVerificationBudgetBurstExceedsMostExpensiveMessage(t *testing.T) {
	require.Greater(t, verificationBudgetBurst, maxPeerMessageCost)

	budget := newVerificationBudget(config.MinVerificationRateLimit)
	require.True(t, budget.Allow(1), "spend a token so the bucket is no longer full")
	require.True(t, budget.Allow(maxPeerMessageCost),
		"the most expensive message must be admissible on a bucket that is not completely full")
}

// The configured minimum rate and the cost model must agree: a budget that
// cannot refill the most expensive message within a second stalls the vote path
// instead of shedding load.
func TestMinimumVerificationRateCoversMostExpensiveMessage(t *testing.T) {
	require.Equal(t, float64(maxPeerMessageCost), float64(config.MinVerificationRateLimit))
}

func TestVerificationBudgetWaitAdmitsMessageAfterRefill(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	clock := clockwork.NewFakeClock()
	budget := newVerificationBudget(300, withVerificationBudgetClock(clock))
	drainVerificationBudget(budget)

	require.True(t, admitAfterAdvance(ctx, t, clock, budget, 10, 50*time.Millisecond),
		"a message that is not affordable yet must wait for the refill, not be dropped")
	require.GreaterOrEqual(t, budget.limiter.TokensAt(clock.Now()), float64(10),
		"the wait must return only once the whole message is affordable")
}

// A wait that ignored cancellation would keep the consensus goroutine from ever
// returning: the queue reader's shutdown path blocks on it.
func TestVerificationBudgetWaitReturnsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clock := clockwork.NewFakeClock()
	budget := newVerificationBudget(300, withVerificationBudgetClock(clock))
	drainVerificationBudget(budget)

	admitted := make(chan bool, 1)
	go func() { admitted <- budget.waitFor(ctx, 10) }()

	require.NoError(t, clock.BlockUntilContext(ctx, 1))
	cancel()

	select {
	case ok := <-admitted:
		require.False(t, ok, "a canceled wait must not admit the message")
	case <-time.After(5 * time.Second):
		t.Fatal("waiting for verification budget did not return after cancellation")
	}
}

// The wait is bounded: past the deadline the message is dropped rather than
// holding the consensus goroutine.
func TestVerificationBudgetWaitGivesUpAtDeadline(t *testing.T) {
	clock := clockwork.NewFakeClock()
	budget := newVerificationBudget(300, withVerificationBudgetClock(clock))
	budget.maxWait = 0
	drainVerificationBudget(budget)

	require.False(t, budget.waitFor(context.Background(), 10))
}

// A cost the bucket can never hold would otherwise be waited on until the
// deadline, once per message, for nothing.
func TestVerificationBudgetWaitRefusesUnaffordableCost(t *testing.T) {
	clock := clockwork.NewFakeClock()
	budget := newVerificationBudget(300, withVerificationBudgetClock(clock))

	require.False(t, budget.waitFor(context.Background(), verificationBudgetBurst+1))
}

// An implementation that peeked at the bucket and dropped whatever it could not
// immediately afford would starve expensive messages completely: a drop costs
// microseconds, so a flood of cheap messages holds the level below the cost of
// one expensive message forever. Waiting turns the same traffic into a delay.
func TestVerificationBudgetWaitAdmitsExpensiveMessageUnderCheapFlood(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const (
		extensions = 4
		rounds     = 8
	)
	clock := clockwork.NewFakeClock()
	budget := newVerificationBudget(300, withVerificationBudgetClock(clock))

	cost, err := budgetedMessageCost(&VoteMessage{Vote: testPrecommitVote(extensions)})
	require.NoError(t, err)
	require.Equal(t, 1+extensions, cost)

	peekAdmissions := 0
	for round := 0; round < rounds; round++ {
		// Cheap messages take every token the moment it refills.
		for budget.Allow(1) {
		}
		if budget.limiter.TokensAt(clock.Now()) >= float64(cost) {
			peekAdmissions++
		}

		require.True(t, admitAfterAdvance(ctx, t, clock, budget, cost, 50*time.Millisecond),
			"expensive message starved in round %d: cheap traffic must delay it, not exclude it", round)

		// Every staged charge of an admitted message must succeed: nothing can
		// take the tokens between the wait and the charges.
		for _, staged := range []int{1, extensions} {
			require.True(t, budget.Allow(staged), "staged charge %d denied in round %d", staged, round)
		}
	}

	require.Zero(t, peekAdmissions,
		"the flood must keep the bucket below the expensive message's cost, "+
			"so an implementation that dropped instead of waiting would admit none of them")
}

// The verification budget covers the whole message, so the staged charges made
// while a commit is verified cannot fail once it has been admitted.
func TestVerificationBudgetCoversCommitVerificationDraws(t *testing.T) {
	const extensions = 3

	quorumHash := crypto.RandQuorumHash()
	privKey := bls12381.GenPrivKey()
	validator := types.NewValidatorDefaultVotingPower(privKey.PubKey(), crypto.RandProTxHash())
	valSet := types.NewValidatorSet(
		[]*types.Validator{validator}, validator.PubKey, btcjson.LLMQType_5_60, quorumHash, true, nil)
	chainID := "verification-budget-commit-test"

	vote := testPrecommitVote(extensions)
	vote.ValidatorProTxHash = validator.ProTxHash
	signData, err := types.MakeQuorumSigns(chainID, btcjson.LLMQType_5_60, quorumHash, vote.ToProto())
	require.NoError(t, err)
	signs, err := signData.SignWithPrivkey(privKey)
	require.NoError(t, err)
	require.NoError(t, vote.VoteExtensions.SetSignatures(signs.VoteExtensionSignatures))
	commit := types.NewCommit(vote.Height, vote.Round, vote.BlockID, vote.VoteExtensions,
		&types.CommitSigns{QuorumSigns: signs, QuorumHash: quorumHash})

	cost, err := budgetedMessageCost(&CommitMessage{Commit: commit})
	require.NoError(t, err)

	budget := &recordingVerificationBudget{}
	require.NoError(t, valSet.VerifyCommitWithBudget(chainID, vote.BlockID, vote.Height, commit, budget))

	require.Equal(t, []int{1, extensions}, budget.charges(),
		"a commit is verified once: the threshold block signature, then its threshold extensions")
	require.GreaterOrEqual(t, cost, sumInts(budget.charges()),
		"the admitted cost must cover every charge the message goes on to make")
}

// admitAfterAdvance runs a bounded wait for cost concurrently with the clock
// advance that would satisfy it, and reports whether the message was admitted.
// An implementation that decides without waiting never registers with the clock;
// the returned decision is then simply whatever it decided.
func admitAfterAdvance(
	ctx context.Context,
	t *testing.T,
	clock *clockwork.FakeClock,
	budget *rateVerificationBudget,
	cost int,
	advance time.Duration,
) bool {
	t.Helper()
	admitted := make(chan bool, 1)
	go func() { admitted <- budget.waitFor(ctx, cost) }()

	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = clock.BlockUntilContext(waitCtx, 1)
	clock.Advance(advance)
	return <-admitted
}

func drainVerificationBudget(budget *rateVerificationBudget) {
	for budget.Allow(1) {
	}
}

func sumInts(values []int) int {
	total := 0
	for _, v := range values {
		total += v
	}
	return total
}

// testPrecommitVote builds an unsigned precommit for a real block carrying n
// threshold-recoverable vote extensions.
func testPrecommitVote(extensions int) *types.Vote {
	exts := make([]*tmproto.VoteExtension, extensions)
	for i := range exts {
		exts[i] = &tmproto.VoteExtension{
			Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
			Extension: []byte(fmt.Sprintf("extension-%d", i)),
		}
	}
	ve, err := types.VoteExtensionsFromProto(exts...)
	if err != nil {
		panic(err)
	}
	return &types.Vote{
		Type:               tmproto.PrecommitType,
		Height:             1,
		Round:              0,
		BlockID:            factory.MakeBlockID(),
		ValidatorProTxHash: crypto.RandProTxHash(),
		VoteExtensions:     ve,
	}
}

// recordingVerificationBudget records every charge, the goroutine that made it
// and its outcome, delegating to a real budget so the recorded outcomes are the
// real ones. It is charged from the consensus goroutine and read from the test
// goroutine, hence the mutex.
type recordingVerificationBudget struct {
	inner *rateVerificationBudget

	mtx     sync.Mutex
	costs   []int
	waits   []int
	goIDs   []uint64
	allowed []bool
}

func (b *recordingVerificationBudget) Allow(cost int) bool {
	allowed := b.inner.Allow(cost)
	id := goroutineID()

	b.mtx.Lock()
	defer b.mtx.Unlock()
	b.costs = append(b.costs, cost)
	b.allowed = append(b.allowed, allowed)
	if !slices.Contains(b.goIDs, id) {
		b.goIDs = append(b.goIDs, id)
	}
	return allowed
}

func (b *recordingVerificationBudget) waitFor(ctx context.Context, cost int) bool {
	b.mtx.Lock()
	b.waits = append(b.waits, cost)
	b.mtx.Unlock()
	return b.inner.waitFor(ctx, cost)
}

// charges reports every draw the node ASKED for, whether or not the budget
// granted it.
func (b *recordingVerificationBudget) charges() []int {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	return slices.Clone(b.costs)
}

// spent reports the work the node actually performed: the draws the budget
// granted, and only those.
//
// A refused draw takes no tokens and does no signature verification, so
// counting it as work would overstate what a run cost — and, worse, would let a
// test that means to prove the node was loaded be satisfied by a node that was
// refused everything it asked for.
func (b *recordingVerificationBudget) spent() int {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	total := 0
	for i, cost := range b.costs {
		if b.allowed[i] {
			total += cost
		}
	}
	return total
}

func (b *recordingVerificationBudget) waitedFor() []int {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	return slices.Clone(b.waits)
}

func (b *recordingVerificationBudget) outcomes() []bool {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	return slices.Clone(b.allowed)
}

func (b *recordingVerificationBudget) goroutines() []uint64 {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	return slices.Clone(b.goIDs)
}

func goroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	var id uint64
	_, _ = fmt.Sscanf(string(buf[:n]), "goroutine %d ", &id)
	return id
}
