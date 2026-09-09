package consensus

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/test/factory"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// What a message costs this node is decided by what it makes the node verify,
// not by what it declares. That distinction is the whole of the staged-permit
// design: a sender that declares the maximum extensions but cannot produce a
// block signature is stopped at the first check and pays for that one check
// alone, so declaring more buys an attacker nothing.
//
// The table records both numbers for every shape a peer can send. Reserved is
// what the scheduler makes room for before dispatching — necessarily the
// declared upper bound, since nothing is known before verification. Charged is
// what the node actually spent. Where the two diverge is where an attacker
// would otherwise have had leverage.
func TestLoadStagedPermitsChargeVerifiedWorkNotDeclaredWork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	const validExtensions = 4

	testCases := []struct {
		name string
		// build returns the message and how much work the sender declared it
		// might cost.
		build func(ctx context.Context, t *testing.T, h *floodHarness) Message
		// reserved is what the scheduler makes room for from the declaration
		// alone.
		reserved int
		// charged is the verification work the node may actually spend. The
		// bound is exact: a message that costs more has found work the cost
		// model does not price.
		charged int
	}{
		{
			// The headline abuse: declare the protocol maximum, produce no
			// usable block signature. Charging the declaration would cost this
			// node 33 verifications for one real check.
			name: "precommit declaring the maximum extensions with an unusable block signature",
			build: func(ctx context.Context, t *testing.T, h *floodHarness) Message {
				return &VoteMessage{Vote: unsignedPrecommit(ctx, t, h, types.MaxVoteExtensions)}
			},
			reserved: maxPrecommitCost,
			charged:  baseMessageCost,
		},
		{
			name: "commit declaring the maximum extensions with an unusable threshold signature",
			build: func(_ context.Context, _ *testing.T, h *floodHarness) Message {
				stateData := h.stateData()
				return &CommitMessage{Commit: forgedCommitWithExtensions(&stateData, types.MaxVoteExtensions)}
			},
			reserved: maxCommitCost,
			charged:  baseMessageCost,
		},
		{
			// A real block signature buys the sender the extension pass, and
			// nothing beyond it: the extensions are drawn for as one stage, so
			// which of them is broken makes no difference to the price.
			name: "precommit with a good block signature and a broken first extension",
			build: func(ctx context.Context, t *testing.T, h *floodHarness) Message {
				return &VoteMessage{Vote: precommitWithBrokenExtension(ctx, t, h, validExtensions, 0)}
			},
			reserved: baseMessageCost + validExtensions,
			charged:  baseMessageCost + validExtensions,
		},
		{
			name: "precommit with a good block signature and a broken final extension",
			build: func(ctx context.Context, t *testing.T, h *floodHarness) Message {
				return &VoteMessage{Vote: precommitWithBrokenExtension(ctx, t, h, validExtensions, validExtensions-1)}
			},
			reserved: baseMessageCost + validExtensions,
			charged:  baseMessageCost + validExtensions,
		},
		{
			// The honest case, and the one that sets what normal operation
			// costs: a precommit this node accepts is verified once, before the
			// application is asked about its extensions, and the vote set
			// stores it on the evidence of that verification rather than
			// repeating it.
			name: "fully valid remote precommit",
			build: func(ctx context.Context, t *testing.T, h *floodHarness) Message {
				stateData := h.stateData()
				return &VoteMessage{Vote: signPrecommitWithExtensions(ctx, t, h.vss[1], &stateData, validExtensions)}
			},
			reserved: baseMessageCost + validExtensions,
			charged:  baseMessageCost + validExtensions,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			h := newFloodHarness(ctx, t, floodHarnessArgs{validators: 4})
			msg := tc.build(ctx, t, h)

			reserved, err := budgetedMessageCost(msg)
			require.NoError(t, err)
			require.Equal(t, tc.reserved, reserved,
				"the scheduler must make room for everything the message declares")

			_ = h.dispatch(ctx, t, msg, "peer")

			charged := h.chargedWork()
			reportf(t, "%s: declared/reserved %d work, charged %d work (%v)",
				tc.name, reserved, charged, h.budget.charges())
			require.Equal(t, tc.charged, charged,
				"the node spent verification work the staged permits are supposed to prevent")
		})
	}
}

// A message the node refuses before dispatch must cost nothing at all — no
// write-ahead log record above everything else, because a record is a disk
// write per attacker message now and a re-verification of the same attacker
// message at every restart later. A message the node does dispatch is written
// once, whether or not it then turns out to be worthless.
//
// The three counts only mean
// something read side by side: zero alone could be a message that never
// arrived, and one alone could be a message written twice on a path the test
// did not take.
func TestLoadWALAccounting(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	// Shed at admission: the budget cannot cover the message and the scheduler
	// gives up on it. Nothing advances the clock, so it never becomes
	// affordable.
	t.Run("shed at admission writes nothing", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		h := newFloodHarness(ctx, t, floodHarnessArgs{validators: 4})
		stateData := h.stateData()

		// Nothing advances the clock while this runs, so the budget never
		// refills and the message stays unaffordable for the whole test rather
		// than being dispatched a few milliseconds later.
		drainVerificationBudget(h.inner)
		stop := h.startWithoutBudgetClock(ctx)
		defer stop()

		require.NoError(t, h.cs.msgInfoQueue.send(ctx,
			&VoteMessage{Vote: signPrecommitWithExtensions(ctx, t, h.vss[1], &stateData, 4)}, "peer"))
		require.NoError(t, h.clock.BlockUntilContext(ctx, 1), "the message must be waiting for budget")

		writes := h.wal.count()
		reportf(t, "WAL records for a message shed at admission: %d", writes)
		require.Zero(t, writes, "a message shed before dispatch must cost no write-ahead log record")
		require.Empty(t, h.budget.charges(),
			"the message was verified after all, so nothing was held at admission and "+
				"the count above is of a message the node never refused")
	})

	// Admitted and then found worthless: one record, written before the
	// verification that rejects it.
	t.Run("admitted then invalid writes once", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		h := newFloodHarness(ctx, t, floodHarnessArgs{validators: 4})
		_ = h.dispatch(ctx, t, &VoteMessage{Vote: unsignedPrecommit(ctx, t, h, 4)}, "peer")

		writes := h.wal.count()
		reportf(t, "WAL records for an admitted message that fails verification: %d", writes)
		require.Equal(t, 1, writes,
			"an admitted message is written once, before the verification that rejects it")
	})

	// Replayed from the log: the record is already there, and writing it again
	// would grow the log at every restart.
	t.Run("replay writes nothing", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		h := newFloodHarness(ctx, t, floodHarnessArgs{validators: 4})
		invalid := unsignedPrecommit(ctx, t, h, 4)

		// Control: the same message off the wire is written, so a later zero
		// means replay was exempted rather than the message being ignored.
		_ = h.dispatch(ctx, t, &VoteMessage{Vote: invalid}, "peer")
		require.Equal(t, 1, h.wal.count(), "the control message was not written, so the test proves nothing")

		h.wal.reset()
		_ = h.dispatchReplayed(ctx, t, &VoteMessage{Vote: invalid}, "peer")
		writes := h.wal.count()
		reportf(t, "WAL records for a replayed message: %d", writes)
		require.Zero(t, writes, "replaying a record must not write it again")
	})
}

// Shedding is this node saying it cannot keep up. Reporting the sender for it
// hands the eviction machinery exactly the peers worth keeping: under bounded
// lanes the peer that reaches capacity first is the honest one at full stretch,
// and at maximum connections that is every honest peer at once.
//
// The count is what makes this a load test rather than a unit test: a single
// shed proving nothing was reported is one sample, and the flood here sheds
// thousands.
func TestLoadOverloadSheddingNeverReportsAPeer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	h := newFloodHarness(ctx, t, floodHarnessArgs{validators: 4})

	// Control: this node does report a peer when it can prove the message was
	// forged, and a commit whose threshold signature does not check out is that
	// case. Without it, the zero below could be a queue nothing ever reaches.
	stateData := h.stateData()
	require.NoError(t, h.dispatch(ctx, t, &CommitMessage{Commit: forgedCommit(&stateData)}, "forger"))
	require.NotEmpty(t, h.cs.peerErrorQueue.ch,
		"this node reported nobody for a message it can prove was forged, so the "+
			"assertion below would hold for a path that never reports anyone")
	reportedBefore := len(h.cs.peerErrorQueue.ch)

	// More per peer than one lane holds, and more in total than every lane
	// together holds, so both bounds are reached many times over. Nothing drains
	// the lanes, so every message beyond capacity is shed.
	const perLane = laneCapacity + 88
	h.floodPrevotes(ctx, t, maxConnectionSlots, perLane)

	offered := maxConnectionSlots * perLane
	shed := h.laneDrops.count()
	reportf(t, "offered %d messages across %d lanes: %.0f shed, %d still queued, %.0f peer errors",
		offered, maxConnectionSlots, shed, h.cs.msgInfoQueue.lanes.buffered(), float64(len(h.cs.peerErrorQueue.ch)))

	require.Positive(t, shed, "nothing was shed, so the test never reached the bound it is about")
	require.Len(t, h.cs.peerErrorQueue.ch, reportedBefore,
		"shedding a peer's message under local overload was reported as that peer's fault")
	require.LessOrEqual(t, h.cs.msgInfoQueue.lanes.buffered(), msgQueueSize,
		"the lanes must not hold more than the single queue they replace")
}

// unsignedPrecommit is a precommit for the current height whose block signature
// cannot verify, declaring n vote extensions. It is what an attacker that
// cannot forge a validator signature can produce for free, and the cheapest way
// to make this node run a signature check.
func unsignedPrecommit(ctx context.Context, t *testing.T, h *floodHarness, n int) *types.Vote {
	t.Helper()
	stateData := h.stateData()
	vote := signPrecommitWithExtensions(ctx, t, h.vss[1], &stateData, n)
	vote.BlockSignature = make([]byte, types.SignatureSize)
	return vote
}

// unsignedPrevote is the cheapest message that still forces a signature check:
// a prevote is verified once, over the block signature alone, so every work
// unit it costs this node is a work unit the sender got for nothing. It is the
// flood an attacker would actually run.
//
// Its signature is a real one lifted from another vote rather than a run of
// zeros. Zeros are refused while the curve point is being read, long before any
// pairing, so a flood of them would be charged the same work unit while costing
// a fraction of the time — and the wall-clock test would then be measuring a
// node with CPU to spare that a real flood would not leave it.
func unsignedPrevote(ctx context.Context, t *testing.T, h *floodHarness) *types.Vote {
	t.Helper()
	stateData := h.stateData()
	vote, err := h.vss[1].signVote(ctx, tmproto.PrevoteType, stateData.state.ChainID, factory.MakeBlockID(),
		stateData.Validators.QuorumType, stateData.Validators.QuorumHash, nil)
	require.NoError(t, err)
	vote.BlockSignature = wellFormedWrongSignature(ctx, t, h)
	return vote
}

// wellFormedWrongSignature is a signature that reads as a point on the curve
// and verifies against nothing: one validator's genuine signature over a
// different message. Verifying it costs a full pairing and fails, which is what
// an attacker can produce for free and what the budget is denominated in.
func wellFormedWrongSignature(ctx context.Context, t *testing.T, h *floodHarness) []byte {
	t.Helper()
	stateData := h.stateData()
	// Same signer, a different block: the signature is genuine and covers the
	// wrong hash, which is exactly the shape an attacker can copy off the wire.
	other, err := h.vss[1].signVote(ctx, tmproto.PrevoteType, stateData.state.ChainID, factory.MakeBlockID(),
		stateData.Validators.QuorumType, stateData.Validators.QuorumHash, nil)
	require.NoError(t, err)
	require.Len(t, other.BlockSignature, types.SignatureSize)
	return other.BlockSignature
}

// precommitWithBrokenExtension is a properly signed precommit whose extension
// number broken cannot verify. The block signature is genuine, so the sender
// buys the extension pass and this node pays for every extension before finding
// the one that is wrong.
func precommitWithBrokenExtension(ctx context.Context, t *testing.T, h *floodHarness, n, broken int) *types.Vote {
	t.Helper()
	stateData := h.stateData()
	vote := signPrecommitWithExtensions(ctx, t, h.vss[1], &stateData, n)
	exts := vote.VoteExtensions
	require.Greater(t, len(exts), broken)
	exts[broken].SetSignature(make([]byte, types.SignatureSize))
	return vote
}

// forgedCommitWithExtensions is a commit for the current height declaring n
// threshold vote extensions, with no usable signature anywhere in it.
func forgedCommitWithExtensions(stateData *StateData, n int) *types.Commit {
	commit := forgedCommit(stateData)
	exts := make(tmproto.VoteExtensions, 0, n)
	for i := 0; i < n; i++ {
		exts = append(exts, &tmproto.VoteExtension{
			Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
			Extension: []byte{byte(i)},
			Signature: make([]byte, types.SignatureSize),
		})
	}
	commit.ThresholdVoteExtensions = exts
	return commit
}

// assertNoWorkAboveBudget checks the whole run stayed inside what the budget
// permits: its refill rate for as long as the run lasted, plus the bucket it
// may have started full with.
func assertNoWorkAboveBudget(t *testing.T, h *floodHarness, rate float64, elapsed time.Duration) {
	t.Helper()
	charged := h.chargedWork()
	allowed := budgetAllowance(rate, elapsed)
	reportf(t, "work charged %d of %.0f allowed over %s (%.1f%% of budget)",
		charged, allowed, elapsed, 100*float64(charged)/allowed)
	assert.LessOrEqual(t, float64(charged), allowed,
		"more verification work was charged than the node-wide budget allows")
}
