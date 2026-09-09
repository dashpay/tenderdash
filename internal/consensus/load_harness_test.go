package consensus

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/go-kit/kit/metrics"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/dash"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	"github.com/dashpay/tenderdash/types"
)

// The tests in the load suite drive one node under the flood the whole of this
// hardening exists to survive, and record what it cost. They are the release
// gate: unit tests show each bound is enforced in isolation, and only a run
// with every bound in place at once shows the node still reaches the next
// height while an attacker holds every connection slot.
//
// Everything they measure is measured against a fake clock. Time advances only
// while something waits for verification budget, and only as far as the next
// whole token, so a recorded latency is the delay the budget imposed and not
// the speed of the machine the suite happens to run on. That is what makes the
// numbers reproducible and the thresholds meaningful; the one test that needs
// real elapsed time says so and grades itself accordingly.
//
// WHERE THE FLOOD IS INJECTED DECIDES WHAT THESE TESTS CAN SAY. They put
// messages straight into the peer lanes, which is downstream of the reactor:
// downstream of the per-peer channel limiters, and of protobuf conversion and
// structural validation. So they measure the scheduler, the budget and the cost
// model under a flood that has already been admitted, and they measure nothing
// about what admits it.
//
// Nothing on the vote branch decides admission by who sent the message, so on
// that branch the flood they inject is the flood a connected peer can drive:
// what a peer buys per message is what these numbers say it is, and what it can
// sustain is the per-peer vote-channel allowance, which is a figure this suite
// reports rather than one it exercises.

// maxConnectionSlots is how many peers can hold a lane at once: every
// connection the node accepts, including the upgrade slots.
const maxConnectionSlots = 68

// Every figure this suite reports is denominated in the node's shipped
// defaults, and both sides of every comparison are derived from the same
// constants — so a retuned default would rescale the measurements and the
// thresholds together and nothing would go red. These are the two that matter,
// checked against the configuration the node actually ships with.
func TestLoadSuiteMeasuresTheShippedDefaults(t *testing.T) {
	cfg := config.DefaultConsensusConfig()
	require.Equal(t, float64(verificationRate), cfg.VerificationRateLimit,
		"the suite is measuring against a verification rate this node does not ship, "+
			"so every latency and every quorum figure it reports describes a different node")

	defaults := config.DefaultP2PConfig()
	require.Equal(t, maxConnectionSlots, int(defaults.MaxConnections)+maxConnectionUpgrades,
		"the suite is measuring a flood across a number of connection slots this node "+
			"does not accept, so every lane ratio it sweeps describes a different node")
}

// maxConnectionUpgrades is how many connections the peer manager may hold above
// its limit while it upgrades away lower-scored peers.
const maxConnectionUpgrades = 4

// syncCounter is a metrics counter a test can read while the consensus
// goroutine writes it.
type syncCounter struct {
	mtx   sync.Mutex
	value float64
}

func (c *syncCounter) With(...string) metrics.Counter { return c }

func (c *syncCounter) Add(delta float64) {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	c.value += delta
}

func (c *syncCounter) count() float64 {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	return c.value
}

// floodHarness is one consensus node wired so that every resource a peer flood
// can spend is counted: the verification work charged, the messages the
// scheduler shed, the records reaching the write-ahead log, and the peer errors
// raised.
//
// It holds the node's real budget, real scheduler and real cost model. The only
// substitutions are the ones that make a measurement possible at all — a fake
// clock for the budget, a recording wrapper that delegates every decision to
// the real budget, a write-ahead log that keeps records instead of writing
// them, and counters in place of the discarded metrics.
type floodHarness struct {
	cs  *State
	vss []*validatorStub

	// clock meters the verification budget. Nothing advances it on its own, so
	// a test either advances it explicitly or runs runBudgetClock.
	clock *clockwork.FakeClock
	// inner is the node's real budget; budget is the recording wrapper the node
	// actually holds.
	inner  *rateVerificationBudget
	budget *recordingVerificationBudget
	wal    *recordingWAL

	laneDrops    *syncCounter
	budgetDrops  *syncCounter
	stateDrops   *syncCounter
	partDrops    *syncCounter
	verifyErrors *syncCounter

	// offered counts what a background flood has sent.
	offered offeredCounter
}

type floodHarnessArgs struct {
	// validators is the size of the validator set the node runs in.
	validators int
	// rate is the node-wide verification budget in work units per second. Zero
	// means the shipped default.
	rate float64
	// wallClock meters the verification budget against real time rather than
	// the harness clock.
	//
	// It is for the one test that has to run as a real node does, where the
	// point is that the signature checks, the application round-trip, the log
	// and the round timeouts still fit together under load. Everywhere else the
	// fake clock is what makes a measurement a measurement rather than a
	// reading of whatever machine the suite ran on.
	wallClock bool
	// application is the ABCI application the node runs against. Zero means the
	// in-memory key-value store every other test uses; a test supplies its own
	// when what the application was asked is part of what it measures.
	application abci.Application
}

func newFloodHarness(ctx context.Context, t *testing.T, args floodHarnessArgs) *floodHarness {
	t.Helper()

	if args.validators == 0 {
		args.validators = 4
	}
	if args.rate == 0 {
		args.rate = verificationRate
	}

	h := &floodHarness{
		clock:        clockwork.NewFakeClock(),
		wal:          &recordingWAL{},
		laneDrops:    &syncCounter{},
		budgetDrops:  &syncCounter{},
		stateDrops:   &syncCounter{},
		partDrops:    &syncCounter{},
		verifyErrors: &syncCounter{},
	}
	if args.wallClock {
		h.inner = newVerificationBudget(args.rate)
	} else {
		h.inner = newVerificationBudget(args.rate, withVerificationBudgetClock(h.clock))
	}
	h.budget = &recordingVerificationBudget{inner: h.inner}

	m := NopMetrics()
	m.PeerLaneDrops = h.laneDrops
	m.VerificationBudgetDrops = h.budgetDrops
	m.StateChannelDrops = h.stateDrops
	m.BlockPartProofDrops = h.partDrops
	m.ProposalVerifyFailures = h.verifyErrors

	h.cs, h.vss = makeState(ctx, t, makeStateArgs{
		validators:  args.validators,
		application: args.application,
		stateOpts: []StateOption{
			WithVerificationBudget(h.budget),
			StateMetrics(m),
		},
	})
	h.cs.wal = h.wal
	return h
}

func (h *floodHarness) stateData() StateData { return h.cs.GetStateData() }

// runCtx carries this node's identity, which the vote path needs to know which
// votes are its own.
func (h *floodHarness) runCtx(ctx context.Context) context.Context {
	return dash.ContextWithProTxHash(ctx, h.cs.privValidator.ProTxHash)
}

// dispatch hands one peer message straight to the consensus goroutine's
// dispatcher and returns when the node has finished with it.
//
// It bypasses the scheduler on purpose. What the staged permits charge is a
// property of the verification path alone, and running it synchronously is what
// makes the recorded charges attributable to one message instead of to whatever
// else happened to be in flight.
func (h *floodHarness) dispatch(ctx context.Context, t *testing.T, msg Message, peerID types.NodeID) error {
	t.Helper()
	stateData := h.cs.GetStateData()
	err := h.cs.msgDispatcher.dispatch(h.runCtx(ctx), &stateData,
		msgInfo{Msg: msg, PeerID: peerID, ReceiveTime: tmtime.Now()})
	_ = stateData.Save()
	return err
}

// dispatchReplayed is dispatch for a message coming back off the write-ahead
// log rather than off the wire.
func (h *floodHarness) dispatchReplayed(ctx context.Context, t *testing.T, msg Message, peerID types.NodeID) error {
	t.Helper()
	stateData := h.cs.GetStateData()
	err := h.cs.msgDispatcher.dispatch(h.runCtx(ctx), &stateData,
		msgInfo{Msg: msg, PeerID: peerID, ReceiveTime: tmtime.Now()}, msgFromReplay())
	_ = stateData.Save()
	return err
}

// start runs the node's receive routine and keeps the budget clock moving,
// returning a function that stops both and waits for them.
func (h *floodHarness) start(ctx context.Context) func() {
	stopped := h.startWithoutBudgetClock(ctx)
	stopClock := advanceBudgetClock(h.runCtx(ctx), h.clock, h.inner)
	return func() {
		stopClock()
		stopped()
	}
}

// startWithoutBudgetClock runs the node's receive routine and leaves the clock
// alone, so a message the budget cannot cover right now can never become
// affordable.
//
// It is what a test needs when the point is what the node does NOT do: with the
// clock advancing, a message that looks unaffordable is dispatched a few
// milliseconds later and an assertion about having refused it becomes a race
// against the refill.
func (h *floodHarness) startWithoutBudgetClock(ctx context.Context) func() {
	runCtx, stop := context.WithCancel(h.runCtx(ctx))
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		h.cs.receiveRoutine(runCtx, nil)
	}()
	return func() {
		stop()
		<-returned
	}
}

// floodLanes fills lanes with the cheapest message that still forces a
// signature verification: a commit for the current height whose threshold
// signature cannot check out.
//
// Nothing de-duplicates a commit that fails verification, so every copy costs
// its sender's lane a turn and this node a signature check — which is the most
// budget an attacker can hold down per message it sends, and therefore the
// flood worth measuring against.
func (h *floodHarness) floodLanes(ctx context.Context, t *testing.T, lanes, perLane int) {
	t.Helper()
	stateData := h.cs.GetStateData()
	for i := 0; i < lanes; i++ {
		peerID := attackerID(i)
		for j := 0; j < perLane; j++ {
			require.NoError(t, h.cs.msgInfoQueue.send(ctx, &CommitMessage{Commit: forgedCommit(&stateData)}, peerID))
		}
	}
}

// floodPrevotes fills lanes with the cheapest message an attacker can make this
// node verify: a prevote for the current height whose block signature cannot
// check out.
//
// It is the flood an attacker would actually run. A prevote is verified once,
// over one signature, so every work unit it takes from the node is a work unit
// the sender produced for free — and unlike a forged commit it is not
// attributable, so sending it costs the attacker nothing at the connection
// level either.
func (h *floodHarness) floodPrevotes(ctx context.Context, t *testing.T, lanes, perLane int) {
	t.Helper()
	vote := unsignedPrevote(ctx, t, h)
	for i := 0; i < lanes; i++ {
		peerID := attackerID(i)
		for j := 0; j < perLane; j++ {
			require.NoError(t, h.cs.msgInfoQueue.send(ctx, &VoteMessage{Vote: vote.Copy()}, peerID))
		}
	}
}

func attackerID(i int) types.NodeID {
	return types.NodeID(fmt.Sprintf("attacker-%03d", i))
}

// reset forgets the records written so far, so a later count is attributable to
// what came after it.
func (w *recordingWAL) reset() {
	w.mtx.Lock()
	defer w.mtx.Unlock()
	w.msgs = nil
}

// chargedWork is the verification work the node has actually performed for
// peer messages so far, in work units.
//
// It counts granted draws only. A refused draw takes no tokens and runs no
// signature check, so counting one would both overstate what a run cost and let
// an assertion meant to show the node was loaded be satisfied by a node that
// was refused everything.
func (h *floodHarness) chargedWork() int {
	return h.budget.spent()
}

// budgetAllowance is the most work the budget permits over elapsed: its refill
// rate for that long, plus the bucket it may have started full with.
func budgetAllowance(rate float64, elapsed time.Duration) float64 {
	return rate*elapsed.Seconds() + verificationBudgetBurst
}

// reportf records a measured number in the test log. The load suite exists to
// produce numbers as much as verdicts: a threshold that passes says the bound
// held, and the number says by how much.
func reportf(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Logf("MEASURED "+format, args...)
}
