package evidence_test

import (
	"context"
	"sync"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/dashpay/dashd-go/btcjson"
	"github.com/fortytw2/leaktest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/eventbus"
	"github.com/dashpay/tenderdash/internal/evidence"
	"github.com/dashpay/tenderdash/internal/evidence/mocks"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

// nopIterator is a ChannelIterator that immediately signals end-of-stream.
// processEvidenceCh calls Receive then iterates; we need a non-nil iterator
// that never produces envelopes so the receive loop is a clean no-op.
type nopIterator struct{}

func (nopIterator) Next(_ context.Context) bool { return false }
func (nopIterator) Envelope() *p2p.Envelope     { return nil }

// recordingChannel is a p2p.Channel stub that records every Send call without
// delivering the message. It is used to verify that syncEvidence re-sends
// evidence on every ticker tick independently of whether the peer actually
// received it (simulating a silently-dropped first delivery).
type recordingChannel struct {
	mu    sync.Mutex
	sends []p2p.Envelope
}

func (rc *recordingChannel) Send(_ context.Context, env p2p.Envelope) error {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.sends = append(rc.sends, env)
	return nil
}
func (rc *recordingChannel) Err() error                                         { return nil }
func (rc *recordingChannel) SendError(_ context.Context, _ p2p.PeerError) error { return nil }
func (rc *recordingChannel) Receive(_ context.Context) p2p.ChannelIterator      { return nopIterator{} }
func (rc *recordingChannel) String() string                                     { return "recordingChannel" }

func (rc *recordingChannel) sendCount() int {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return len(rc.sends)
}

// TestSyncEvidenceRetries is the deterministic regression guard for the
// peer-routing-race fix (PR #1366).
//
// It verifies that syncEvidence re-sends pooled evidence on every ticker tick,
// not just once on initial peer connection. This is the critical property that
// recovers from the race where PeerStatusUp fires before the p2p channel route
// is ready — the first burst of sends is silently dropped but the retry loop
// delivers evidence on the next tick.
//
// # Why this test is deterministic
//
// The recording channel captures every Send call without delivering it. We
// assert that the total send count exceeds numEv (the pool size), which can
// only happen if the goroutine walked the pool MORE THAN ONCE — i.e., the
// ticker fired at least once after the initial walk.
//
// # Pre-fix (one-shot walk) behavior
//
// Before the ticker loop was introduced, syncEvidence walked the pool once then
// returned. Send would be called exactly numEv times. The assertion
// `count > numEv` would FAIL, making this a true regression guard.
//
// # Post-fix (ticker loop) behavior
//
// The goroutine re-sends all pending evidence on every tick. After a few
// ticks the count grows well beyond numEv and the assertion passes.
func TestSyncEvidenceRetries(t *testing.T) {
	const (
		tickInterval = 20 * time.Millisecond
		numEv        = 2
		// Overall deadline: give the retry loop a very generous budget so the
		// test is robust on slow/loaded CI runners. The condition is polled
		// every half-tick, so on a healthy runner it completes in < 100 ms.
		waitFor = 5 * time.Second
	)

	// Speed up the ticker so multiple ticks occur within milliseconds.
	restore := evidence.SetEvidenceSyncIntervalForTesting(tickInterval)
	defer restore()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Goroutine-leak check: must be registered before the reactor so it runs
	// after the reactor is stopped (t.Cleanup is LIFO).
	t.Cleanup(leaktest.Check(t))

	// Build a minimal evidence pool backed by the test state.
	quorumHash := crypto.RandQuorumHash()
	val := types.NewMockPVForQuorum(quorumHash)
	height := int64(numEv) + 10
	stateDB := initializeValidatorState(ctx, t, val, height, btcjson.LLMQType_5_60, quorumHash)

	evidenceDB := dbm.NewMemDB()
	blockStore := &mocks.BlockStore{}
	state, err := stateDB.Load()
	require.NoError(t, err)
	evidenceTime := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)
	blockStore.On("Base").Return(int64(1))
	blockStore.On("Height").Return(state.LastBlockHeight)
	blockStore.On("LoadBlockMeta", mock.AnythingOfType("int64")).Return(
		func(h int64) *types.BlockMeta {
			if h <= state.LastBlockHeight {
				return makeBlockMeta(h, evidenceTime, state.Validators)
			}
			return nil
		},
	)
	eventBus := eventbus.NewDefault(log.NewNopLogger())
	require.NoError(t, eventBus.Start(ctx))

	pool := evidence.NewPool(log.NewNopLogger(), evidenceDB, stateDB, blockStore, evidence.NopMetrics(), eventBus)
	startPool(t, pool, stateDB)

	// Add evidence to the primary pool.
	evList := createEvidenceList(ctx, t, pool, val, numEv)
	_ = evList

	// Wire the reactor with the recording channel so every Send is captured.
	rc := &recordingChannel{}
	peerChan := make(chan p2p.PeerUpdate)
	pu := p2p.NewPeerUpdates(peerChan, 1, "evidence")
	reactor := evidence.NewReactor(
		log.NewNopLogger(),
		func(_ context.Context, _ *p2p.ChannelDescriptor) (p2p.Channel, error) { return rc, nil },
		func(_ context.Context, _ string) *p2p.PeerUpdates { return pu },
		pool,
	)
	require.NoError(t, reactor.Start(ctx))
	t.Cleanup(func() {
		reactor.Stop()
		reactor.Wait()
	})

	// Inject a PeerStatusUp for a fake peer; this starts the syncEvidence goroutine.
	// The send blocks until processPeerUpdates reads it, so by the time it returns
	// the goroutine is already running.
	fakePeerID := types.NodeID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	peerChan <- p2p.PeerUpdate{Status: p2p.PeerStatusUp, NodeID: fakePeerID}

	// Poll until the send count exceeds numEv, proving the ticker fired at
	// least once after the initial walk. require.Eventually is robust to
	// scheduler jitter on slow/loaded CI runners — unlike time.Sleep it does
	// not fail if ticks arrive slightly late.
	//
	// A pre-fix one-shot goroutine would send exactly numEv then exit, so the
	// condition would never become true — true regression guard.
	require.Eventually(t, func() bool {
		return rc.sendCount() > numEv
	}, waitFor, tickInterval/2,
		"syncEvidence must re-send evidence on every ticker tick; "+
			"pre-fix one-shot code would send exactly numEv=%d then exit", numEv)

	count := rc.sendCount()
	t.Logf("evidence retry verified: %d sends for %d pooled items at %v interval",
		count, numEv, tickInterval)
}
