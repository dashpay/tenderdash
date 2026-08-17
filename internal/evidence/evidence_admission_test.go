package evidence_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/dashpay/dashd-go/btcjson"
	"github.com/fortytw2/leaktest"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/eventbus"
	"github.com/dashpay/tenderdash/internal/evidence"
	"github.com/dashpay/tenderdash/internal/p2p"
	smmocks "github.com/dashpay/tenderdash/internal/state/mocks"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// countingBlockStore is the work oracle for these tests. verify() calls
// LoadBlockMeta before anything else it does, so a message that reaches
// verification increments this counter exactly once and a message refused by
// admission does not increment it at all. Counting the disk lookup rather than
// the BLS call also measures the second resource admission is meant to bound.
type countingBlockStore struct {
	loads  atomic.Int64
	bounds atomic.Int64
	height atomic.Int64
	meta   func(height int64) *types.BlockMeta
}

func (s *countingBlockStore) LoadBlockMeta(height int64) *types.BlockMeta {
	s.loads.Add(1)
	return s.meta(height)
}
func (s *countingBlockStore) LoadBlockCommit(int64) *types.Commit { return nil }

// Base and Height build a database iterator in the real store, so they are
// counted: admission must not reach them on the per-message path.
func (s *countingBlockStore) Base() int64 {
	s.bounds.Add(1)
	return 1
}
func (s *countingBlockStore) Height() int64 {
	s.bounds.Add(1)
	return s.height.Load()
}

// errorRecordingChannel records broadcasts and peer errors so a test can assert
// that a drop was non-punitive.
type errorRecordingChannel struct {
	mu     sync.Mutex
	sends  []p2p.Envelope
	errors []p2p.PeerError
}

func (c *errorRecordingChannel) Send(_ context.Context, env p2p.Envelope) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sends = append(c.sends, env)
	return nil
}
func (c *errorRecordingChannel) Err() error { return nil }
func (c *errorRecordingChannel) SendError(_ context.Context, e p2p.PeerError) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errors = append(c.errors, e)
	return nil
}
func (c *errorRecordingChannel) Receive(_ context.Context) p2p.ChannelIterator { return nopIterator{} }
func (c *errorRecordingChannel) String() string                                { return "errorRecordingChannel" }

func (c *errorRecordingChannel) sendCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sends)
}

func (c *errorRecordingChannel) errorCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.errors)
}

// admissionHarness is one evidence reactor wired to a real pool, a real
// validator key, a counting block store and a fake clock, driven one envelope
// at a time. The fake clock is what makes the rate assertions exact: no tokens
// refill unless the test asks for them.
type admissionHarness struct {
	reactor    *evidence.Reactor
	pool       *evidence.Pool
	ch         *errorRecordingChannel
	blockStore *countingBlockStore
	clock      *clockwork.FakeClock
	val        types.PrivValidator
	height     int64
}

const admissionHarnessHeight = int64(20)

func newAdmissionHarness(ctx context.Context, t *testing.T) *admissionHarness {
	t.Helper()

	// Registered first so it runs last: the reactor must be stopped before we
	// look for stray goroutines.
	t.Cleanup(leaktest.Check(t))

	quorumHash := crypto.RandQuorumHash()
	val := types.NewMockPVForQuorum(quorumHash)
	stateDB := initializeValidatorState(ctx, t, val, admissionHarnessHeight, btcjson.LLMQType_5_60, quorumHash)
	state, err := stateDB.Load()
	require.NoError(t, err)

	blockStore := &countingBlockStore{}
	blockStore.height.Store(admissionHarnessHeight)
	blockStore.meta = func(h int64) *types.BlockMeta {
		if h <= blockStore.height.Load() {
			return makeBlockMeta(h, defaultEvidenceTime, state.Validators)
		}
		return nil
	}

	eventBus := eventbus.NewDefault(log.NewNopLogger())
	require.NoError(t, eventBus.Start(ctx))

	pool := evidence.NewPool(log.NewNopLogger(), dbm.NewMemDB(), stateDB, blockStore, evidence.NopMetrics(), eventBus)
	startPool(t, pool, stateDB)

	ch := &errorRecordingChannel{}
	peerChan := make(chan p2p.PeerUpdate)
	pu := p2p.NewPeerUpdates(peerChan, 1, "evidence")
	clock := clockwork.NewFakeClock()
	reactor := evidence.NewReactor(
		log.NewNopLogger(),
		func(_ context.Context, _ *p2p.ChannelDescriptor) (p2p.Channel, error) { return ch, nil },
		func(_ context.Context, _ string) *p2p.PeerUpdates { return pu },
		pool,
		evidence.WithAdmissionClock(clock),
	)
	require.NoError(t, reactor.Start(ctx))
	t.Cleanup(func() {
		reactor.Stop()
		reactor.Wait()
	})

	return &admissionHarness{
		reactor:    reactor,
		pool:       pool,
		ch:         ch,
		blockStore: blockStore,
		clock:      clock,
		val:        val,
		height:     admissionHarnessHeight,
	}
}

// evidenceAt builds valid DuplicateVoteEvidence signed by the harness validator.
func (h *admissionHarness) evidenceAt(ctx context.Context, t *testing.T, height int64) *types.DuplicateVoteEvidence {
	t.Helper()
	vals := h.pool.State().Validators
	ev, err := types.NewMockDuplicateVoteEvidenceWithValidator(
		ctx, height, defaultEvidenceTime, h.val, evidenceChainID, vals.QuorumType, vals.QuorumHash)
	require.NoError(t, err)
	return ev
}

// deliver feeds one envelope to the reactor exactly as processEvidenceCh would.
func (h *admissionHarness) deliver(
	ctx context.Context,
	t *testing.T,
	peer types.NodeID,
	msg *tmproto.Evidence,
) error {
	t.Helper()
	return h.reactor.HandleEvidenceMessageForTest(ctx, &p2p.Envelope{
		ChannelID: evidence.EvidenceChannel,
		From:      peer,
		Message:   msg,
	})
}

func toProto(t *testing.T, ev types.Evidence) *tmproto.Evidence {
	t.Helper()
	pb, err := types.EvidenceToProto(ev)
	require.NoError(t, err)
	return pb
}

// mutateSignature returns the evidence with byte i of VoteA's block signature
// flipped: a different wire message and a different evidence hash, alleging the
// very same equivocation.
func mutateSignature(t *testing.T, ev types.Evidence, i int) *tmproto.Evidence {
	t.Helper()
	pb := toProto(t, ev)
	sig := pb.GetDuplicateVoteEvidence().VoteA.BlockSignature
	require.NotEmpty(t, sig)
	mutated := make([]byte, len(sig))
	copy(mutated, sig)
	mutated[i%len(mutated)] ^= 0xff
	pb.GetDuplicateVoteEvidence().VoteA.BlockSignature = mutated
	return pb
}

// fabricate returns evidence alleging a wholly new equivocation: both block
// IDs are rewritten, so no de-duplication can recognize it and only the work
// budget stands between the sender and a verification. The leading bytes keep
// the votes in the lexicographic order ValidateBasic requires.
func fabricate(t *testing.T, ev types.Evidence, i int) *tmproto.Evidence {
	t.Helper()
	pb := toProto(t, ev)
	dve := pb.GetDuplicateVoteEvidence()
	stamp := func(hash []byte, lead byte) []byte {
		out := make([]byte, len(hash))
		copy(out, hash)
		out[0] = lead
		binary.BigEndian.PutUint32(out[1:5], uint32(i))
		return out
	}
	dve.VoteA.BlockID.Hash = stamp(dve.VoteA.BlockID.Hash, 0x00)
	dve.VoteB.BlockID.Hash = stamp(dve.VoteB.BlockID.Hash, 0xff)
	return pb
}

func peerID(n int) types.NodeID {
	return types.NodeID(fmt.Sprintf("%040x", n))
}

// TestByteFlipFloodDoesNotReverify is the core regression guard. De-duplication
// keyed on the evidence hash, which covers the signature bytes, so flipping one
// byte produced "new" evidence and bought the attacker a full re-verification —
// two disk lookups and two BLS pairings — for the cost of one byte.
//
// Once the node holds a verified piece of evidence, every mutation of it
// alleges an equivocation the node has already proven, so it must cost nothing.
func TestByteFlipFloodDoesNotReverify(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newAdmissionHarness(ctx, t)

	genuine := h.evidenceAt(ctx, t, h.height-1)
	require.NoError(t, h.pool.AddEvidence(ctx, genuine))
	require.EqualValues(t, 1, h.pool.Size())

	baseline := h.blockStore.loads.Load()
	boundsBaseline := h.blockStore.bounds.Load()

	const flood = 200
	for i := 0; i < flood; i++ {
		require.NoError(t, h.deliver(ctx, t, peerID(1), mutateSignature(t, genuine, i)),
			"a mutated copy must never produce a peer error")
	}

	assert.Equal(t, baseline, h.blockStore.loads.Load(),
		"a byte-flip flood of evidence we already hold must not cost a single verification")
	assert.Equal(t, boundsBaseline, h.blockStore.bounds.Load(),
		"nor may it ask the block store for its bounds — each of those is an iterator")
	assert.EqualValues(t, 1, h.pool.Size(), "no mutated copy may enter the pool")
}

// TestPoisonedIdentityDoesNotSuppressGenuineEvidence pins the safety property
// that dominates the design: evidence is how equivocating validators get
// punished, so a defense that lets an attacker suppress genuine evidence is
// worse than the flood it prevents.
//
// The attacker knows the equivocation — it is public gossip — and sends it
// first with a corrupted signature, trying to poison the de-duplication key.
// Because only evidence that survives verification is ever remembered, the
// genuine item that follows must still be accepted and gossiped. This test
// fails against any design that remembers failures.
func TestPoisonedIdentityDoesNotSuppressGenuineEvidence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newAdmissionHarness(ctx, t)

	genuine := h.evidenceAt(ctx, t, h.height-1)

	_ = h.deliver(ctx, t, peerID(9), mutateSignature(t, genuine, 0))
	require.Zero(t, h.pool.Size(), "forged evidence must not enter the pool")

	require.NoError(t, h.deliver(ctx, t, peerID(1), toProto(t, genuine)))
	assert.EqualValues(t, 1, h.pool.Size(),
		"genuine evidence must still be accepted after an attacker poisoned its identity")
	assert.NotZero(t, h.ch.sendCount(), "accepted evidence must still be gossiped")
}

// TestGenuineEvidenceAdmittedUnderFlood is the liveness half of the same
// property. Sixty attacker identities empty every bucket they can reach with
// fabricated evidence; an honest peer's genuine item must still land.
//
// It may be refused at the node-wide ceiling on first contact — that ceiling
// exists precisely so a synchronized burst cannot be absorbed — so the test
// models what a real sender does: syncEvidence re-sends every pending item once
// a second, forever. A drop must therefore be a delay, never a loss.
func TestGenuineEvidenceAdmittedUnderFlood(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newAdmissionHarness(ctx, t)

	base := h.evidenceAt(ctx, t, h.height-1)
	blamed := 0
	for peer := 1; peer <= 60; peer++ {
		for i := 0; i < 40; i++ {
			if err := h.deliver(ctx, t, peerID(peer), fabricate(t, base, peer*1000+i)); err != nil {
				blamed++
			}
		}
	}
	flooded := h.blockStore.loads.Load()
	assert.NotZero(t, flooded, "the flood must have been admitted up to the budget, not refused outright")
	assert.Less(t, flooded, int64(400),
		"a 2400-message flood must not convert into 2400 verifications")
	// Only a message that was admitted and then failed verification is blamed on
	// its sender, and admission caps how many of those there can be. Everything
	// shed is silent.
	assert.LessOrEqual(t, blamed, nodeBudgetMessages,
		"no shed message may be reported as a peer error")

	honest := h.evidenceAt(ctx, t, h.height-2)
	for attempt := 0; attempt < 3 && h.pool.Size() == 0; attempt++ {
		require.NoError(t, h.deliver(ctx, t, peerID(1000), toProto(t, honest)),
			"an honest peer must never be blamed for arriving during a flood")
		h.clock.Advance(time.Second) // the honest sender's next sync tick
	}
	assert.EqualValues(t, 1, h.pool.Size(),
		"an honest peer's genuine evidence must land within a few sync ticks of a maximal flood")
}

// TestFabricatedFloodIsBounded proves the ceiling exists at all: fabricated
// evidence cannot be de-duplicated, so the only thing between an attacker and
// unbounded verification work is the work budget.
//
// Evidence that is admitted and then fails verification still reports an
// invalid-evidence error — that is the reactor's existing response to a bad
// signature and is left as it was. What matters here is that the number of
// messages reaching verification at all is bounded by the sender's budget.
func TestFabricatedFloodIsBounded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newAdmissionHarness(ctx, t)

	base := h.evidenceAt(ctx, t, h.height-1)
	const flood = 500
	verified := 0
	for i := 0; i < flood; i++ {
		if err := h.deliver(ctx, t, peerID(7), fabricate(t, base, i)); err != nil {
			verified++
		}
	}

	assert.Equal(t, peerEvidenceBurstMessages, verified,
		"one peer gets exactly what its budget buys — no more, and not zero either")
	assert.Less(t, h.blockStore.loads.Load(), int64(flood/4),
		"one peer must not convert a flood into unbounded verification work")
	assert.Zero(t, h.pool.Size())
}

// TestSheddingIsNeverPunitive is the guardrail on every refusal this gate makes.
// processEvidenceCh turns any error returned by the handler into a p2p.PeerError,
// which evicts the peer — so a shed message must return nil. A node under load
// refusing to spend work on a message says nothing about whether the sender is
// honest, and the honest sender is exactly the one who keeps re-sending.
func TestSheddingIsNeverPunitive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newAdmissionHarness(ctx, t)

	// Drain the sender's budget with evidence we already hold, which is refused
	// for free, then with well-formed evidence it cannot pay for.
	genuine := h.evidenceAt(ctx, t, h.height-1)
	require.NoError(t, h.pool.AddEvidence(ctx, genuine))

	base := h.evidenceAt(ctx, t, h.height-2)
	for i := 0; i < peerEvidenceBurstMessages; i++ {
		_ = h.deliver(ctx, t, peerID(2), fabricate(t, base, i))
	}

	// Everything from here on is shed rather than verified.
	loadsBefore := h.blockStore.loads.Load()
	require.NotZero(t, loadsBefore, "the drain must have spent the budget, not found it already empty")
	for i := 0; i < 100; i++ {
		require.NoError(t, h.deliver(ctx, t, peerID(2), fabricate(t, base, 1000+i)),
			"a message refused for lack of budget must not be reported as a peer error")
	}
	require.Equal(t, loadsBefore, h.blockStore.loads.Load(),
		"the shed messages must not have been verified")
	// Structural guard: the handler must leave peer blame to its caller, which
	// only ever sees the returned error.
	assert.Zero(t, h.ch.errorCount(), "the handler must never raise a peer error itself")
}

// How many pieces of evidence each budget's burst pays for. Kept here rather
// than exported so the production constants stay internal.
const (
	peerEvidenceBurstMessages = 8
	nodeBudgetMessages        = 80
)

// TestUnverifiableEvidenceIsNotRememberedAsAnIdentity closes the one way
// de-duplication could have been turned into a suppression weapon.
//
// VerifyDuplicateVote skips the signature checks when the validator set at the
// evidence height carries no public keys — a real situation for a node that
// joined the quorum recently — and the evidence is stored regardless. Were such
// evidence remembered, an attacker could take a real equivocation (they are
// public), attach garbage signatures, claim its identity, and the genuine proof
// would be refused ever after.
//
// So acceptance alone must not be enough to be remembered: the signatures must
// actually have been checked.
func TestUnverifiableEvidenceIsNotRememberedAsAnIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	t.Cleanup(leaktest.Check(t))

	const height = int64(20)
	quorumHash := crypto.RandQuorumHash()
	val := types.NewMockPVForQuorum(quorumHash)
	proTxHash, err := val.GetProTxHash(ctx)
	require.NoError(t, err)
	pubKey, err := val.GetPubKey(ctx, quorumHash)
	require.NoError(t, err)

	current := &types.ValidatorSet{
		Validators:         []*types.Validator{{VotingPower: types.DefaultDashVotingPower, PubKey: pubKey, ProTxHash: proTxHash}},
		ThresholdPublicKey: pubKey,
		QuorumType:         btcjson.LLMQType_5_60,
		QuorumHash:         quorumHash,
		HasPublicKeys:      true,
	}
	// The validator set at the evidence height is one we joined after: we know
	// who was in it but not their keys.
	keyless := &types.ValidatorSet{
		Validators:    []*types.Validator{{VotingPower: types.DefaultDashVotingPower, ProTxHash: proTxHash}},
		QuorumType:    btcjson.LLMQType_5_60,
		QuorumHash:    quorumHash,
		HasPublicKeys: false,
	}

	stateStore := &smmocks.Store{}
	stateStore.On("Load").Return(createState(height, current), nil)
	stateStore.On("LoadValidators", mock.AnythingOfType("int64"), mock.Anything).Return(keyless, nil)

	blockStore := &countingBlockStore{}
	blockStore.height.Store(height)
	blockStore.meta = func(h int64) *types.BlockMeta {
		if h <= height {
			return makeBlockMeta(h, defaultEvidenceTime, current)
		}
		return nil
	}

	eventBus := eventbus.NewDefault(log.NewNopLogger())
	require.NoError(t, eventBus.Start(ctx))
	pool := evidence.NewPool(log.NewNopLogger(), dbm.NewMemDB(), stateStore, blockStore, evidence.NopMetrics(), eventBus)
	startPool(t, pool, stateStore)

	peerChan := make(chan p2p.PeerUpdate)
	pu := p2p.NewPeerUpdates(peerChan, 1, "evidence")
	reactor := evidence.NewReactor(
		log.NewNopLogger(),
		func(_ context.Context, _ *p2p.ChannelDescriptor) (p2p.Channel, error) {
			return &errorRecordingChannel{}, nil
		},
		func(_ context.Context, _ string) *p2p.PeerUpdates { return pu },
		pool,
	)
	require.NoError(t, reactor.Start(ctx))
	t.Cleanup(func() {
		reactor.Stop()
		reactor.Wait()
	})

	genuine, err := types.NewMockDuplicateVoteEvidenceWithValidator(
		ctx, height-1, defaultEvidenceTime, val, evidenceChainID, btcjson.LLMQType_5_60, quorumHash)
	require.NoError(t, err)

	send := func(peer types.NodeID, msg *tmproto.Evidence) {
		_ = reactor.HandleEvidenceMessageForTest(ctx, &p2p.Envelope{
			ChannelID: evidence.EvidenceChannel, From: peer, Message: msg,
		})
	}

	// The attacker gets there first with a corrupted copy. That it is stored at
	// all is a pre-existing hole in verification, not something this gate
	// introduces — what matters is what happens next.
	send(peerID(9), mutateSignature(t, genuine, 0))
	require.EqualValues(t, 1, pool.Size(), "unverifiable evidence is stored today; this pins that premise")

	send(peerID(1), toProto(t, genuine))
	assert.EqualValues(t, 2, pool.Size(),
		"a copy we could not verify must never claim an identity and lock the genuine proof out")
}

// TestBlockValidationIsNotRateLimited draws the boundary of this whole
// mechanism. Admission governs what we accept from gossip; block validation
// must stay a pure function of the block and the chain state.
//
// If a drained budget — or a de-duplication memory whose contents depend on
// which gossip this node happened to see — could make CheckEvidence reject,
// two honest nodes would disagree about the same block and the chain would
// halt. That is a far worse failure than the flood this change prevents.
func TestBlockValidationIsNotRateLimited(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newAdmissionHarness(ctx, t)

	// Empty every budget the gossip path has.
	base := h.evidenceAt(ctx, t, h.height-1)
	for peer := 1; peer <= 60; peer++ {
		for i := 0; i < 40; i++ {
			_ = h.deliver(ctx, t, peerID(peer), fabricate(t, base, peer*1000+i))
		}
	}

	proposed := h.evidenceAt(ctx, t, h.height-3)
	require.NoError(t, h.pool.CheckEvidence(ctx, types.EvidenceList{proposed}),
		"a block's evidence must be validated regardless of the gossip work budget")
	require.EqualValues(t, 1, h.pool.Size())

	// A second, unrelated piece in a later block must validate too: nothing the
	// gossip path did — spending the budgets, remembering an identity — may
	// carry over into what a block is allowed to contain.
	another := h.evidenceAt(ctx, t, h.height-4)
	require.NoError(t, h.pool.CheckEvidence(ctx, types.EvidenceList{another}),
		"block validation must not inherit any state from the gossip path")
	require.EqualValues(t, 2, h.pool.Size())
}

// TestRefusingAnUnservableHeightIsADelay is the liveness half of the height
// window. Evidence for a height the block store cannot serve is refused, but
// the sender keeps offering it — so once we have the block, the very same
// evidence must be accepted rather than stay locked out.
func TestRefusingAnUnservableHeightIsADelay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newAdmissionHarness(ctx, t)

	ahead := h.evidenceAt(ctx, t, h.height+1)
	require.NoError(t, h.deliver(ctx, t, peerID(3), toProto(t, ahead)))
	require.Zero(t, h.pool.Size(), "evidence for a height we cannot serve is refused")
	require.Zero(t, h.blockStore.loads.Load(), "and refused before any disk lookup")

	// We reach the height. The peer's next sync tick offers the same item again.
	h.blockStore.height.Store(h.height + 1)
	state := h.pool.State()
	state.LastBlockHeight = h.height + 1
	h.pool.Update(ctx, state, nil)

	require.NoError(t, h.deliver(ctx, t, peerID(3), toProto(t, ahead)))
	assert.EqualValues(t, 1, h.pool.Size(),
		"once we hold the block, the evidence we refused must be accepted")
}

// TestOnePeersFloodDoesNotSpendAnother is the per-peer reservation itself: the
// property that lets a node refuse a flood without refusing the honest peer
// standing behind it. Buckets are per sender, so emptying one leaves the rest
// untouched however many identities an attacker holds.
func TestOnePeersFloodDoesNotSpendAnother(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newAdmissionHarness(ctx, t)

	base := h.evidenceAt(ctx, t, h.height-1)
	for i := 0; i < peerEvidenceBurstMessages+20; i++ {
		_ = h.deliver(ctx, t, peerID(1), fabricate(t, base, i))
	}
	drained := h.blockStore.loads.Load()

	// The attacker is now empty: nothing more from it reaches verification.
	_ = h.deliver(ctx, t, peerID(1), fabricate(t, base, 9999))
	require.Equal(t, drained, h.blockStore.loads.Load(), "the flooding peer must be out of budget")

	honest := h.evidenceAt(ctx, t, h.height-2)
	require.NoError(t, h.deliver(ctx, t, peerID(2), toProto(t, honest)))
	assert.EqualValues(t, 1, h.pool.Size(),
		"a second peer's evidence must be admitted on its own budget")
}

// TestOutOfWindowEvidenceCostsNoDiskIO pins the cheap-rejection ordering: a
// height above our tip provably has no block, so refusing it must not reach the
// block store.
func TestOutOfWindowEvidenceCostsNoDiskIO(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newAdmissionHarness(ctx, t)

	ev := h.evidenceAt(ctx, t, h.height+5)
	baseline := h.blockStore.loads.Load()

	require.NoError(t, h.deliver(ctx, t, peerID(3), toProto(t, ev)))
	assert.Equal(t, baseline, h.blockStore.loads.Load(),
		"evidence for a height above our tip must be refused before any disk lookup")
}

// TestStructurallyInconsistentEvidenceCostsNoDiskIO covers the other free
// rejection: VerifyDuplicateVote requires both votes to agree on height, round,
// type and validator, but reaches those checks only after two disk lookups.
func TestStructurallyInconsistentEvidenceCostsNoDiskIO(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newAdmissionHarness(ctx, t)

	ev := h.evidenceAt(ctx, t, h.height-1)
	pb := toProto(t, ev)
	pb.GetDuplicateVoteEvidence().VoteB.Round++

	baseline := h.blockStore.loads.Load()
	require.NoError(t, h.deliver(ctx, t, peerID(4), pb))
	assert.Equal(t, baseline, h.blockStore.loads.Load(),
		"votes that do not describe one equivocation must be refused before any disk lookup")
}

// TestCommittedEvidenceMutationIsFree checks that the identity memory survives
// the pending→committed transition: evidence already punished on chain must not
// become a fresh verification target because a byte changed.
func TestCommittedEvidenceMutationIsFree(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newAdmissionHarness(ctx, t)

	genuine := h.evidenceAt(ctx, t, h.height-1)
	require.NoError(t, h.pool.AddEvidence(ctx, genuine))

	state := h.pool.State()
	state.LastBlockHeight++
	state.LastBlockTime = defaultEvidenceTime.Add(time.Minute)
	h.pool.Update(ctx, state, types.EvidenceList{genuine})
	require.Zero(t, h.pool.Size(), "evidence must have moved to the committed set")

	baseline := h.blockStore.loads.Load()
	for i := 0; i < 50; i++ {
		require.NoError(t, h.deliver(ctx, t, peerID(5), mutateSignature(t, genuine, i)))
	}
	assert.Equal(t, baseline, h.blockStore.loads.Load(),
		"mutations of committed evidence must not force re-verification")
}
