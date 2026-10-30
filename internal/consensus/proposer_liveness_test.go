package consensus

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/internal/consensus/versioned/selectproposer"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

const livenessTestChainID = "liveness-test-chain"

type livenessFixture struct {
	valSet *types.ValidatorSet
	// privVals[i] signs for valSet.Validators[i].
	privVals []types.PrivValidator
	emitter  *eventemitter.EventEmitter
	liveness *proposerLiveness
}

func newLivenessFixture(t *testing.T, ready func(context.Context, int64) error) *livenessFixture {
	t.Helper()
	valSet, privVals := types.RandValidatorSet(4)
	emitter := eventemitter.New()
	return &livenessFixture{
		valSet:   valSet,
		privVals: privVals,
		emitter:  emitter,
		liveness: newProposerLiveness(log.NewNopLogger(), NopMetrics(), emitter, ready, nil),
	}
}

func (f *livenessFixture) target(proposer *types.Validator, silence time.Duration) livenessTarget {
	return livenessTarget{
		height:     7,
		round:      0,
		proposer:   proposer,
		chainID:    livenessTestChainID,
		quorumType: f.valSet.QuorumType,
		quorumHash: f.valSet.QuorumHash,
		interval:   silence / 5,
		silence:    silence,
	}
}

func (f *livenessFixture) privValidator(t *testing.T, val *types.Validator) privValidator {
	t.Helper()
	index, _ := f.valSet.GetByProTxHash(val.ProTxHash)
	require.GreaterOrEqual(t, index, int32(0))
	return privValidator{PrivValidator: f.privVals[index], ProTxHash: val.ProTxHash}
}

// heartbeat returns a heartbeat for the fixture's round, signed by signer in
// the name of named.
func (f *livenessFixture) heartbeat(
	t *testing.T,
	signer *types.Validator,
	named *types.Validator,
	sequence uint64,
	at time.Time,
) *types.ProposerHeartbeat {
	t.Helper()
	return f.heartbeatIn(t, 0, signer, named, sequence, at)
}

// heartbeatIn returns a heartbeat for round of the fixture's height, signed by
// signer in the name of named.
func (f *livenessFixture) heartbeatIn(
	t *testing.T,
	round int32,
	signer *types.Validator,
	named *types.Validator,
	sequence uint64,
	at time.Time,
) *types.ProposerHeartbeat {
	t.Helper()
	pb := &tmproto.ProposerHeartbeat{
		Height:            7,
		Round:             round,
		Sequence:          sequence,
		ProposerProTxHash: named.ProTxHash,
		Timestamp:         at,
	}
	pv := f.privValidator(t, signer)
	require.NoError(t, pv.SignProposerHeartbeat(
		context.Background(), livenessTestChainID, f.valSet.QuorumType, f.valSet.QuorumHash, pb))
	hb, err := types.ProposerHeartbeatFromProto(pb)
	require.NoError(t, err)
	return hb
}

func TestProposerLivenessWatch(t *testing.T) {
	t.Run("should report the proposer silent when no heartbeat arrives", func(t *testing.T) {
		f := newLivenessFixture(t, nil)
		proposer := f.valSet.Validators[0]
		f.liveness.watch(f.target(proposer, 100*time.Millisecond))
		defer f.liveness.stop()

		select {
		case silence := <-f.liveness.silent():
			assert.Equal(t, int64(7), silence.Height)
			assert.Equal(t, int32(0), silence.Round)
			assert.Equal(t, proposer.ProTxHash, silence.Proposer)
		case <-time.After(2 * time.Second):
			t.Fatal("proposer not reported silent")
		}
	})

	t.Run("should stay quiet while heartbeats arrive", func(t *testing.T) {
		f := newLivenessFixture(t, nil)
		proposer := f.valSet.Validators[0]
		silence := 300 * time.Millisecond
		f.liveness.watch(f.target(proposer, silence))
		defer f.liveness.stop()

		// Three times the silence timeout, with a heartbeat every third of it.
		for seq := uint64(1); seq <= 9; seq++ {
			relay, err := f.liveness.heard(f.heartbeat(t, proposer, proposer, seq, tmtime.Now()))
			require.NoError(t, err)
			assert.True(t, relay, "heartbeat %d", seq)
			select {
			case <-f.liveness.silent():
				t.Fatalf("proposer reported silent after heartbeat %d", seq)
			case <-time.After(silence / 3):
			}
		}
	})

	t.Run("should not report silence from a timer a heartbeat re-armed", func(t *testing.T) {
		f := newLivenessFixture(t, nil)
		silence := 50 * time.Millisecond
		f.liveness.watch(f.target(f.valSet.Validators[0], silence))
		defer f.liveness.stop()

		// Hold the lock past the deadline, so the timer fires and its callback
		// waits for the lock, then re-arm as an accepted heartbeat does.
		f.liveness.mtx.Lock()
		time.Sleep(2 * silence)
		f.liveness.lastSeq = 1
		f.liveness.armLocked()
		f.liveness.mtx.Unlock()

		select {
		case <-f.liveness.silent():
			t.Fatal("a timer re-armed by a heartbeat reported silence")
		case <-time.After(silence / 2):
		}
	})

	t.Run("should treat a report as stale once a heartbeat arrives", func(t *testing.T) {
		f := newLivenessFixture(t, nil)
		proposer := f.valSet.Validators[0]
		f.liveness.watch(f.target(proposer, 50*time.Millisecond))
		defer f.liveness.stop()

		var report proposerSilence
		select {
		case report = <-f.liveness.silent():
		case <-time.After(2 * time.Second):
			t.Fatal("proposer not reported silent")
		}
		assert.True(t, f.liveness.current(report))

		relay, err := f.liveness.heard(f.heartbeat(t, proposer, proposer, 1, tmtime.Now()))
		require.NoError(t, err)
		require.True(t, relay)
		assert.False(t, f.liveness.current(report), "a report raised before the heartbeat must be stale")
	})

	t.Run("should not report silence after the watch stops", func(t *testing.T) {
		f := newLivenessFixture(t, nil)
		f.liveness.watch(f.target(f.valSet.Validators[0], 50*time.Millisecond))
		f.liveness.stop()

		select {
		case <-f.liveness.silent():
			t.Fatal("stopped watch reported silence")
		case <-time.After(200 * time.Millisecond):
		}
	})
}

func TestProposerLivenessHeard(t *testing.T) {
	// Each case watches afresh: a round's heartbeats are paced (see
	// TestProposerLivenessHeartbeatPace).
	watching := func(t *testing.T) (f *livenessFixture, proposer, other *types.Validator) {
		f = newLivenessFixture(t, nil)
		proposer, other = f.valSet.Validators[0], f.valSet.Validators[1]
		f.liveness.watch(f.target(proposer, time.Minute))
		t.Cleanup(f.liveness.stop)
		return f, proposer, other
	}

	t.Run("should relay only the first copy of a heartbeat", func(t *testing.T) {
		f, proposer, _ := watching(t)
		hb := f.heartbeat(t, proposer, proposer, 1, tmtime.Now())
		relay, err := f.liveness.heard(hb)
		require.NoError(t, err)
		assert.True(t, relay)

		relay, err = f.liveness.heard(hb)
		require.NoError(t, err)
		assert.False(t, relay)
	})

	t.Run("should hear a fresh heartbeat with a lower sequence", func(t *testing.T) {
		f, proposer, _ := watching(t)
		// A proposer restarted within the same millisecond, or whose clock went
		// back, signs sequences below those it sent before.
		relay, err := f.liveness.heard(f.heartbeat(t, proposer, proposer, 5, tmtime.Now()))
		require.NoError(t, err)
		assert.True(t, relay)

		relay, err = f.liveness.heard(f.heartbeat(t, proposer, proposer, 4, tmtime.Now()))
		require.NoError(t, err)
		assert.True(t, relay, "a recent heartbeat not seen before is news whatever its sequence")
	})

	t.Run("should hear a heartbeat that repeats a sequence at another time", func(t *testing.T) {
		f, proposer, _ := watching(t)
		signed := tmtime.Now()
		relay, err := f.liveness.heard(f.heartbeat(t, proposer, proposer, 7, signed))
		require.NoError(t, err)
		assert.True(t, relay)

		relay, err = f.liveness.heard(f.heartbeat(t, proposer, proposer, 7, signed.Add(time.Millisecond)))
		require.NoError(t, err)
		assert.True(t, relay, "a restarted proposer can sign a sequence again")
	})

	t.Run("should refuse a heartbeat from a validator that is not the proposer", func(t *testing.T) {
		f, _, other := watching(t)
		relay, err := f.liveness.heard(f.heartbeat(t, other, other, 10, tmtime.Now()))
		assert.ErrorIs(t, err, errProposerHeartbeatWrongProposer)
		assert.False(t, relay)
	})

	t.Run("should refuse a heartbeat forged in the proposer's name", func(t *testing.T) {
		f, proposer, other := watching(t)
		relay, err := f.liveness.heard(f.heartbeat(t, other, proposer, 11, tmtime.Now()))
		assert.ErrorIs(t, err, types.ErrProposerHeartbeatInvalidSignature)
		assert.False(t, relay)
	})

	t.Run("should ignore a heartbeat signed too long ago", func(t *testing.T) {
		f, proposer, _ := watching(t)
		relay, err := f.liveness.heard(f.heartbeat(t, proposer, proposer, 12, tmtime.Now().Add(-2*time.Minute)))
		require.NoError(t, err)
		assert.False(t, relay)
	})

	t.Run("should ignore a heartbeat for another round", func(t *testing.T) {
		f, proposer, _ := watching(t)
		hb := f.heartbeat(t, proposer, proposer, 13, tmtime.Now())
		hb.Round = 1
		relay, err := f.liveness.heard(hb)
		require.NoError(t, err)
		assert.False(t, relay)
	})

	t.Run("should drop a heartbeat the verification budget cannot afford", func(t *testing.T) {
		g := newLivenessFixture(t, nil)
		budget := &tokenBudget{}
		g.liveness.budget = budget
		g.liveness.watch(g.target(g.valSet.Validators[0], time.Minute))
		defer g.liveness.stop()

		forged := g.heartbeat(t, g.valSet.Validators[1], g.valSet.Validators[0], 1, tmtime.Now())
		relay, err := g.liveness.heard(forged)
		require.NoError(t, err, "a heartbeat that was not verified cannot be refused as forged")
		assert.False(t, relay)

		budget.tokens = baseMessageCost
		_, err = g.liveness.heard(forged)
		assert.ErrorIs(t, err, types.ErrProposerHeartbeatInvalidSignature)
		assert.Equal(t, baseMessageCost, budget.charged)
	})

	t.Run("should ignore heartbeats while nothing is watched", func(t *testing.T) {
		g := newLivenessFixture(t, nil)
		relay, err := g.liveness.heard(g.heartbeat(t, g.valSet.Validators[0], g.valSet.Validators[0], 1, tmtime.Now()))
		require.NoError(t, err)
		assert.False(t, relay)
	})
}

// A proposer can sign as many distinct heartbeats as it likes, and every
// validator relays each one it accepts, so a round's heartbeats are paced:
// one past the pace is dropped before it is verified, and is not relayed.
func TestProposerLivenessHeartbeatPace(t *testing.T) {
	watching := func(t *testing.T, silence time.Duration) (*livenessFixture, *tokenBudget) {
		f := newLivenessFixture(t, nil)
		budget := &tokenBudget{tokens: 100 * baseMessageCost}
		f.liveness.budget = budget
		f.liveness.watch(f.target(f.valSet.Validators[0], silence))
		t.Cleanup(f.liveness.stop)
		return f, budget
	}

	t.Run("should drop a heartbeat past the pace without verifying it", func(t *testing.T) {
		// A minute's silence paces heartbeats six seconds apart, far longer
		// than the test runs.
		f, budget := watching(t, time.Minute)
		proposer := f.valSet.Validators[0]
		for seq := uint64(1); seq <= heartbeatPaceBurst; seq++ {
			relay, err := f.liveness.heard(f.heartbeat(t, proposer, proposer, seq, tmtime.Now()))
			require.NoError(t, err)
			require.True(t, relay, "heartbeat %d", seq)
		}

		hb := f.heartbeat(t, proposer, proposer, heartbeatPaceBurst+1, tmtime.Now())
		wanted, err := f.liveness.wanted(hb)
		require.NoError(t, err)
		assert.False(t, wanted, "not worth a place in the sender's lane")
		relay, err := f.liveness.heard(hb)
		require.NoError(t, err)
		assert.False(t, relay)
		assert.Equal(t, heartbeatPaceBurst*baseMessageCost, budget.charged, "only the paced heartbeats were verified")
	})

	t.Run("should not count a forged heartbeat against the pace", func(t *testing.T) {
		f, _ := watching(t, time.Minute)
		proposer, other := f.valSet.Validators[0], f.valSet.Validators[1]
		for seq := uint64(1); seq <= heartbeatPaceBurst+1; seq++ {
			_, err := f.liveness.heard(f.heartbeat(t, other, proposer, seq, tmtime.Now()))
			require.ErrorIs(t, err, types.ErrProposerHeartbeatInvalidSignature)
		}
		relay, err := f.liveness.heard(f.heartbeat(t, proposer, proposer, 10, tmtime.Now()))
		require.NoError(t, err)
		assert.True(t, relay, "forgeries must not use up the proposer's pace")
	})

	t.Run("should pace each round on its own", func(t *testing.T) {
		f, _ := watching(t, time.Minute)
		proposer, next := f.valSet.Validators[0], f.valSet.Validators[1]
		f.liveness.relay = &relayRounds{
			height:     7,
			chainID:    livenessTestChainID,
			quorumType: f.valSet.QuorumType,
			quorumHash: f.valSet.QuorumHash,
			timeouts:   types.TimeoutParams{ProposerHeartbeat: 12 * time.Second, ProposerSilence: time.Minute},
			proposers:  map[int32]*types.Validator{0: proposer, 1: next},
		}
		for seq := uint64(1); seq <= heartbeatPaceBurst; seq++ {
			_, err := f.liveness.heard(f.heartbeat(t, proposer, proposer, seq, tmtime.Now()))
			require.NoError(t, err)
		}
		relay, err := f.liveness.heard(f.heartbeatIn(t, 1, next, next, 1, tmtime.Now()))
		require.NoError(t, err)
		assert.True(t, relay, "round 0's pace says nothing about round 1")
	})

	t.Run("should accept heartbeats again as the pace allows", func(t *testing.T) {
		// Two seconds' silence paces heartbeats 200ms apart.
		f, _ := watching(t, 2*time.Second)
		proposer := f.valSet.Validators[0]
		for seq := uint64(1); seq <= heartbeatPaceBurst; seq++ {
			_, err := f.liveness.heard(f.heartbeat(t, proposer, proposer, seq, tmtime.Now()))
			require.NoError(t, err)
		}
		time.Sleep(300 * time.Millisecond)
		relay, err := f.liveness.heard(f.heartbeat(t, proposer, proposer, 10, tmtime.Now()))
		require.NoError(t, err)
		assert.True(t, relay)
	})
}

// The reactor screens a peer's heartbeat before queuing it for the consensus
// goroutine. The screen refuses what it can without a signature check, and
// spends nothing on the rest: the check is the consensus goroutine's, made in
// turn with the sender's other messages.
func TestProposerLivenessWanted(t *testing.T) {
	f := newLivenessFixture(t, nil)
	budget := &tokenBudget{tokens: 100 * baseMessageCost}
	f.liveness.budget = budget
	proposer, other := f.valSet.Validators[0], f.valSet.Validators[1]
	f.liveness.watch(f.target(proposer, time.Minute))
	defer f.liveness.stop()

	wanted, err := f.liveness.wanted(f.heartbeat(t, other, proposer, 1, tmtime.Now()))
	require.NoError(t, err)
	assert.True(t, wanted, "a forgery is only found out by the signature check")

	_, err = f.liveness.wanted(f.heartbeat(t, other, other, 2, tmtime.Now()))
	assert.ErrorIs(t, err, errProposerHeartbeatWrongProposer)

	wanted, err = f.liveness.wanted(f.heartbeat(t, proposer, proposer, 3, tmtime.Now().Add(-2*time.Minute)))
	require.NoError(t, err)
	assert.False(t, wanted, "signed too long ago")

	hb := f.heartbeat(t, proposer, proposer, 4, tmtime.Now())
	_, err = f.liveness.heard(hb)
	require.NoError(t, err)
	wanted, err = f.liveness.wanted(hb)
	require.NoError(t, err)
	assert.False(t, wanted, "seen before")

	assert.Equal(t, baseMessageCost, budget.charged, "the screen verifies nothing")
}

func TestProposerLivenessAnnounce(t *testing.T) {
	collect := func(f *livenessFixture) <-chan *types.ProposerHeartbeat {
		ch := make(chan *types.ProposerHeartbeat, 100)
		f.emitter.AddListener(proposerHeartbeatEventName, func(data eventemitter.EventData) error {
			ch <- data.(*types.ProposerHeartbeat)
			return nil
		})
		return ch
	}

	t.Run("should send signed heartbeats with growing sequence until stopped", func(t *testing.T) {
		f := newLivenessFixture(t, nil)
		sent := collect(f)
		proposer := f.valSet.Validators[0]
		target := f.target(proposer, 250*time.Millisecond)
		f.liveness.announce(context.Background(), target, f.privValidator(t, proposer))

		var last uint64
		for i := 0; i < 3; i++ {
			select {
			case hb := <-sent:
				assert.Greater(t, hb.Sequence, last)
				last = hb.Sequence
				assert.Equal(t, proposer.ProTxHash, hb.ProposerProTxHash)
				assert.NoError(t, hb.Verify(livenessTestChainID, f.valSet.QuorumType, f.valSet.QuorumHash, proposer.PubKey))
			case <-time.After(2 * time.Second):
				t.Fatalf("heartbeat %d not sent", i+1)
			}
		}

		f.liveness.stop()
		// One heartbeat may have been signed while stop ran.
		time.Sleep(2 * target.interval)
		for len(sent) > 0 {
			<-sent
		}
		select {
		case hb := <-sent:
			t.Fatalf("heartbeat %d sent after stop", hb.Sequence)
		case <-time.After(3 * target.interval):
		}
	})

	t.Run("should be heard again at once after the proposer restarts", func(t *testing.T) {
		f := newLivenessFixture(t, nil)
		proposer := f.valSet.Validators[0]
		target := f.target(proposer, 250*time.Millisecond)
		f.liveness.watch(target)
		defer f.liveness.stop()

		// announceFrom runs a proposer process until it has sent n heartbeats,
		// and hands each to the watcher.
		announceFrom := func(n int) uint64 {
			emitter := eventemitter.New()
			sender := newProposerLiveness(log.NewNopLogger(), NopMetrics(), emitter, nil, nil)
			sent := make(chan *types.ProposerHeartbeat, 100)
			emitter.AddListener(proposerHeartbeatEventName, func(data eventemitter.EventData) error {
				sent <- data.(*types.ProposerHeartbeat)
				return nil
			})
			sender.announce(context.Background(), target, f.privValidator(t, proposer))
			defer sender.stop()

			var last uint64
			for i := 0; i < n; i++ {
				select {
				case hb := <-sent:
					relay, err := f.liveness.heard(hb)
					require.NoError(t, err)
					assert.True(t, relay, "heartbeat %d of a proposer process was dropped", i+1)
					last = hb.Sequence
				case <-time.After(2 * time.Second):
					t.Fatalf("heartbeat %d not sent", i+1)
				}
			}
			return last
		}

		before := announceFrom(3)
		// The restarted process starts with no memory of what it sent.
		after := announceFrom(1)
		assert.Greater(t, after, before)
	})

	t.Run("should not report its own round silent", func(t *testing.T) {
		f := newLivenessFixture(t, nil)
		proposer := f.valSet.Validators[0]
		f.liveness.announce(context.Background(), f.target(proposer, 50*time.Millisecond), f.privValidator(t, proposer))
		defer f.liveness.stop()

		select {
		case <-f.liveness.silent():
			t.Fatal("announcing node reported its own round silent")
		case <-time.After(200 * time.Millisecond):
		}
	})

	t.Run("should send nothing while the application cannot propose", func(t *testing.T) {
		f := newLivenessFixture(t, func(context.Context, int64) error { return errors.New("application down") })
		sent := collect(f)
		proposer := f.valSet.Validators[0]
		f.liveness.announce(context.Background(), f.target(proposer, 100*time.Millisecond), f.privValidator(t, proposer))
		defer f.liveness.stop()

		select {
		case hb := <-sent:
			t.Fatalf("heartbeat %d sent while the application was down", hb.Sequence)
		case <-time.After(300 * time.Millisecond):
		}
	})
}

func TestHeartbeatSequence(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)

	t.Run("should count from the signing time", func(t *testing.T) {
		assert.Equal(t, uint64(1_700_000_000_000), heartbeatSequence(0, now))
	})

	t.Run("should still grow when the clock has not moved on", func(t *testing.T) {
		assert.Equal(t, uint64(1_700_000_000_001), heartbeatSequence(1_700_000_000_000, now))
		assert.Equal(t, uint64(1_700_000_000_051), heartbeatSequence(1_700_000_000_050, now))
	})

	t.Run("should grow from the last one with a clock before 1970", func(t *testing.T) {
		assert.Equal(t, uint64(8), heartbeatSequence(7, time.UnixMilli(-5)))
	})
}

func TestProposerLivenessSeenHeartbeats(t *testing.T) {
	t.Run("should forget heartbeats older than the silence timeout", func(t *testing.T) {
		l := newProposerLiveness(log.NewNopLogger(), NopMetrics(), eventemitter.New(), nil, nil)
		now := tmtime.Now()
		silence := 5 * time.Second
		l.rememberLocked(heartbeatID{sequence: 1}, now.Add(-2*silence), now, silence)
		l.rememberLocked(heartbeatID{sequence: 2}, now.Add(-silence/2), now, silence)
		l.rememberLocked(heartbeatID{sequence: 3}, now, now, silence)

		assert.NotContains(t, l.seen, heartbeatID{sequence: 1})
		assert.Contains(t, l.seen, heartbeatID{sequence: 2})
		assert.Contains(t, l.seen, heartbeatID{sequence: 3})
	})

	t.Run("should keep at most maxSeenHeartbeats, dropping the oldest", func(t *testing.T) {
		l := newProposerLiveness(log.NewNopLogger(), NopMetrics(), eventemitter.New(), nil, nil)
		now := tmtime.Now()
		for i := 0; i <= maxSeenHeartbeats; i++ {
			signed := now.Add(time.Duration(i) * time.Millisecond)
			l.rememberLocked(heartbeatID{sequence: uint64(i)}, signed, now, time.Minute)
		}
		assert.Len(t, l.seen, maxSeenHeartbeats)
		assert.NotContains(t, l.seen, heartbeatID{sequence: 0})
		assert.Contains(t, l.seen, heartbeatID{sequence: uint64(maxSeenHeartbeats)})
	})
}

// unsupportedHeartbeatSigner refuses every heartbeat as a request it cannot
// sign at all, as remote signers do.
type unsupportedHeartbeatSigner struct {
	types.PrivValidator
	attempts atomic.Int32
}

func (pv *unsupportedHeartbeatSigner) SignProposerHeartbeat(
	context.Context, string, btcjson.LLMQType, crypto.QuorumHash, *tmproto.ProposerHeartbeat,
) error {
	pv.attempts.Add(1)
	return fmt.Errorf("remote signer: %w", types.ErrSigningNotSupported)
}

func TestProposerLivenessUnsupportedSigner(t *testing.T) {
	t.Run("should stop asking a signer that cannot sign heartbeats", func(t *testing.T) {
		f := newLivenessFixture(t, nil)
		sent := make(chan *types.ProposerHeartbeat, 10)
		f.emitter.AddListener(proposerHeartbeatEventName, func(data eventemitter.EventData) error {
			sent <- data.(*types.ProposerHeartbeat)
			return nil
		})
		proposer := f.valSet.Validators[0]
		signer := &unsupportedHeartbeatSigner{PrivValidator: f.privVals[0]}
		pv := privValidator{PrivValidator: signer, ProTxHash: proposer.ProTxHash}

		target := f.target(proposer, 100*time.Millisecond)
		f.liveness.announce(context.Background(), target, pv)
		time.Sleep(10 * target.interval)
		assert.Equal(t, int32(1), signer.attempts.Load(), "a signer that cannot sign heartbeats is asked once")

		// Nor in the next round it proposes.
		target.round = 1
		f.liveness.announce(context.Background(), target, pv)
		defer f.liveness.stop()
		time.Sleep(5 * target.interval)
		assert.Equal(t, int32(1), signer.attempts.Load())
		assert.Empty(t, sent)

		select {
		case <-f.liveness.silent():
			t.Fatal("a proposer reported its own round silent")
		default:
		}
	})
}

func TestProposerLivenessRearm(t *testing.T) {
	t.Run("should report the proposer silent again after a silence it could not act on", func(t *testing.T) {
		f := newLivenessFixture(t, nil)
		f.liveness.watch(f.target(f.valSet.Validators[0], 50*time.Millisecond))
		defer f.liveness.stop()

		first := <-f.liveness.silent()
		f.liveness.rearm(first)
		select {
		case again := <-f.liveness.silent():
			assert.Equal(t, first.Round, again.Round)
		case <-time.After(2 * time.Second):
			t.Fatal("silence not reported again after rearm")
		}
	})

	t.Run("should not rearm a watch a heartbeat re-armed since", func(t *testing.T) {
		f := newLivenessFixture(t, nil)
		proposer := f.valSet.Validators[0]
		silence := 50 * time.Millisecond
		f.liveness.watch(f.target(proposer, silence))
		defer f.liveness.stop()

		stale := <-f.liveness.silent()
		relay, err := f.liveness.heard(f.heartbeat(t, proposer, proposer, 1, tmtime.Now()))
		require.NoError(t, err)
		require.True(t, relay)
		f.liveness.mtx.Lock()
		arm := f.liveness.arm
		f.liveness.mtx.Unlock()

		f.liveness.rearm(stale)
		f.liveness.mtx.Lock()
		assert.Equal(t, arm, f.liveness.arm, "a stale report must not restart the timer")
		f.liveness.mtx.Unlock()
	})
}

func TestProposerReady(t *testing.T) {
	clock := clockwork.NewFakeClock()
	cs := &State{catchup: &catchupTracker{}}
	require.NoError(t, cs.proposerReady(context.Background(), 5))

	// After a block-sync handover to height 9, the propose step holds back
	// until the node is past it, and the heartbeat must not claim otherwise.
	cs.catchup.arm(9, clock)
	assert.ErrorIs(t, cs.proposerReady(context.Background(), 5), errProposerCatchingUp)
	assert.NoError(t, cs.proposerReady(context.Background(), 10))
}

func TestProposerLivenessRelaysNearbyRounds(t *testing.T) {
	f := newLivenessFixture(t, nil)
	watched := f.valSet.Validators[0]
	nextProposer := f.valSet.Validators[1]
	f.liveness.watch(f.target(watched, time.Minute))
	defer f.liveness.stop()
	f.liveness.relay = &relayRounds{
		height:     7,
		chainID:    livenessTestChainID,
		quorumType: f.valSet.QuorumType,
		quorumHash: f.valSet.QuorumHash,
		timeouts:   types.TimeoutParams{}.TimeoutParamsOrDefaults(),
		proposers:  map[int32]*types.Validator{0: watched, 1: nextProposer},
	}
	heartbeatInRound := func(signer, named *types.Validator, round int32) *types.ProposerHeartbeat {
		pb := &tmproto.ProposerHeartbeat{
			Height:            7,
			Round:             round,
			Sequence:          1,
			ProposerProTxHash: named.ProTxHash,
			Timestamp:         tmtime.Now(),
		}
		require.NoError(t, f.privValidator(t, signer).SignProposerHeartbeat(
			context.Background(), livenessTestChainID, f.valSet.QuorumType, f.valSet.QuorumHash, pb))
		hb, err := types.ProposerHeartbeatFromProto(pb)
		require.NoError(t, err)
		return hb
	}

	t.Run("should relay a nearby round's heartbeat without touching its own watch", func(t *testing.T) {
		f.liveness.mtx.Lock()
		arm := f.liveness.arm
		f.liveness.mtx.Unlock()

		hb := heartbeatInRound(nextProposer, nextProposer, 1)
		relay, err := f.liveness.heard(hb)
		require.NoError(t, err)
		assert.True(t, relay, "validators still waiting in round 1 must hear it")

		f.liveness.mtx.Lock()
		assert.Equal(t, arm, f.liveness.arm, "a heartbeat for another round must not re-arm the watch")
		f.liveness.mtx.Unlock()

		relay, err = f.liveness.heard(hb)
		require.NoError(t, err)
		assert.False(t, relay, "relayed once")
	})

	t.Run("should refuse a nearby round's heartbeat in the name of another validator", func(t *testing.T) {
		_, err := f.liveness.heard(heartbeatInRound(watched, watched, 1))
		assert.ErrorIs(t, err, errProposerHeartbeatWrongProposer)
	})

	t.Run("should ignore a heartbeat for a round too far from its own", func(t *testing.T) {
		relay, err := f.liveness.heard(heartbeatInRound(nextProposer, nextProposer, 9))
		require.NoError(t, err)
		assert.False(t, relay)
	})
}

func TestProposerLivenessFollowRounds(t *testing.T) {
	valSet, _ := types.RandValidatorSet(4)
	selector, err := selectproposer.NewHeightRoundProposerSelector(valSet.Copy(), 7, 2, nil, nil)
	require.NoError(t, err)
	current, err := selector.GetProposer(7, 2)
	require.NoError(t, err)
	stateData := &StateData{
		RoundState: cstypes.RoundState{Height: 7, Round: 2, Validators: valSet, ProposerSelector: selector},
		state: sm.State{
			ChainID: livenessTestChainID,
			ConsensusParams: types.ConsensusParams{
				Version: types.VersionParams{ConsensusVersion: int32(tmproto.VersionParams_CONSENSUS_VERSION_2)},
			},
		},
	}

	// What the node's own selector names for each round.
	expected := selector.Copy()

	l := newProposerLiveness(log.NewNopLogger(), NopMetrics(), eventemitter.New(), nil, nil)
	l.followRounds(stateData)

	require.Len(t, l.relay.proposers, int(2+maxRoundSkipRoundsAhead+1), "rounds 0 to the current one and a few ahead")
	for round := int32(0); round <= 2+maxRoundSkipRoundsAhead; round++ {
		proposer, err := expected.GetProposer(7, round)
		require.NoError(t, err)
		assert.Equal(t, proposer.ProTxHash, l.relay.proposers[round].ProTxHash, "round %d", round)
	}

	again, err := selector.GetProposer(7, 2)
	require.NoError(t, err)
	assert.Equal(t, current.ProTxHash, again.ProTxHash, "the consensus selector is left where it was")

	// The window moves with the node's round.
	stateData.Round = 3
	l.followRounds(stateData)
	assert.Equal(t, int32(0), l.relay.first)
	assert.Equal(t, int32(3+maxRoundSkipRoundsAhead), l.relay.last)
	proposer, err := expected.GetProposer(7, 3+maxRoundSkipRoundsAhead)
	require.NoError(t, err)
	require.NotNil(t, l.relay.proposers[3+maxRoundSkipRoundsAhead], "the new last round of the window")
	assert.Equal(t, proposer.ProTxHash, l.relay.proposers[3+maxRoundSkipRoundsAhead].ProTxHash)

	// Paces are kept only for the rounds of the window, however many rounds
	// the height runs to.
	l.mtx.Lock()
	for round := int32(0); round <= 3+maxRoundSkipRoundsAhead; round++ {
		l.paceLocked(round, time.Second)
	}
	l.mtx.Unlock()
	stateData.Round = 3 + 2*maxRoundSkipRoundsAhead
	l.followRounds(stateData)
	l.mtx.Lock()
	defer l.mtx.Unlock()
	for round := range l.paces {
		assert.GreaterOrEqual(t, round, l.relay.first, "pace kept for round %d, which the window left", round)
	}
	assert.NotEmpty(t, l.paces, "the rounds still in the window keep theirs")
}

// The node can leave the round while its signer works on a heartbeat. The
// heartbeat is then dropped: validators still waiting in that round would take
// it as word that the node is about to propose there.
func TestProposerLivenessDropsHeartbeatForARoundLeftWhileSigning(t *testing.T) {
	f := newLivenessFixture(t, nil)
	proposer := f.valSet.Validators[0]
	var emitted atomic.Int32
	f.emitter.AddListener(proposerHeartbeatEventName, func(eventemitter.EventData) error {
		emitted.Add(1)
		return nil
	})
	target := f.target(proposer, time.Minute)
	f.liveness.watch(target)
	f.liveness.mtx.Lock()
	generation := f.liveness.generation
	f.liveness.mtx.Unlock()

	pv := f.privValidator(t, proposer)
	pv.PrivValidator = &hookedHeartbeatSigner{PrivValidator: pv.PrivValidator, signing: f.liveness.stop}
	more := f.liveness.sendHeartbeat(context.Background(), generation, target, pv)
	assert.False(t, more, "the sender for the round left stops")
	assert.Zero(t, emitted.Load(), "no heartbeat goes out for the round left")
}

// hookedHeartbeatSigner calls signing as it starts on each heartbeat.
type hookedHeartbeatSigner struct {
	types.PrivValidator
	signing func()
}

func (pv *hookedHeartbeatSigner) SignProposerHeartbeat(
	ctx context.Context,
	chainID string,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	hb *tmproto.ProposerHeartbeat,
) error {
	pv.signing()
	return pv.PrivValidator.SignProposerHeartbeat(ctx, chainID, quorumType, quorumHash, hb)
}

func TestWantsHeartbeat(t *testing.T) {
	valSet, _ := types.RandValidatorSet(4)
	hb := &types.ProposerHeartbeat{Height: 7, Round: 5}
	peer := func(proTxHash types.ProTxHash, height int64, round int32) *PeerState {
		return &PeerState{ProTxHash: proTxHash, PRS: cstypes.PeerRoundState{Height: height, Round: round}}
	}
	validator := valSet.Validators[1].ProTxHash

	assert.True(t, wantsHeartbeat(peer(validator, 7, 5), valSet, hb), "a validator waiting in its round")
	assert.True(t, wantsHeartbeat(peer(validator, 7, 6), valSet, hb),
		"a validator a round ahead relays it to those still in its round")
	assert.True(t, wantsHeartbeat(peer(validator, 7, 5-maxRoundSkipRoundsAhead), valSet, hb))
	assert.False(t, wantsHeartbeat(peer(validator, 7, 5+maxRoundSkipRoundsAhead+1), valSet, hb), "too far ahead")
	assert.False(t, wantsHeartbeat(peer(validator, 8, 5), valSet, hb), "another height")
	assert.False(t, wantsHeartbeat(peer(crypto.RandProTxHash(), 7, 5), valSet, hb), "not a validator")
}
