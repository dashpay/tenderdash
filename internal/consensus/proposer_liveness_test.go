package consensus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

const livenessTestChainID = "liveness-test-chain"

type livenessFixture struct {
	valSet   *types.ValidatorSet
	privVals map[string]types.PrivValidator
	emitter  *eventemitter.EventEmitter
	liveness *proposerLiveness
}

func newLivenessFixture(t *testing.T, ready func(context.Context) error) *livenessFixture {
	t.Helper()
	valSet, pvs := types.RandValidatorSet(4)
	privVals := make(map[string]types.PrivValidator, len(pvs))
	for _, pv := range pvs {
		proTxHash, err := pv.GetProTxHash(context.Background())
		require.NoError(t, err)
		privVals[proTxHash.String()] = pv
	}
	emitter := eventemitter.New()
	return &livenessFixture{
		valSet:   valSet,
		privVals: privVals,
		emitter:  emitter,
		liveness: newProposerLiveness(log.NewNopLogger(), NopMetrics(), emitter, ready),
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
	pv, ok := f.privVals[val.ProTxHash.String()]
	require.True(t, ok)
	return privValidator{PrivValidator: pv, ProTxHash: val.ProTxHash}
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
	pb := &tmproto.ProposerHeartbeat{
		Height:            7,
		Round:             0,
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
	f := newLivenessFixture(t, nil)
	proposer := f.valSet.Validators[0]
	other := f.valSet.Validators[1]
	f.liveness.watch(f.target(proposer, time.Minute))
	defer f.liveness.stop()

	t.Run("should relay only the first copy of a heartbeat", func(t *testing.T) {
		hb := f.heartbeat(t, proposer, proposer, 1, tmtime.Now())
		relay, err := f.liveness.heard(hb)
		require.NoError(t, err)
		assert.True(t, relay)

		relay, err = f.liveness.heard(hb)
		require.NoError(t, err)
		assert.False(t, relay)
	})

	t.Run("should drop a heartbeat older than the last one", func(t *testing.T) {
		relay, err := f.liveness.heard(f.heartbeat(t, proposer, proposer, 5, tmtime.Now()))
		require.NoError(t, err)
		assert.True(t, relay)

		relay, err = f.liveness.heard(f.heartbeat(t, proposer, proposer, 4, tmtime.Now()))
		require.NoError(t, err)
		assert.False(t, relay)
	})

	t.Run("should refuse a heartbeat from a validator that is not the proposer", func(t *testing.T) {
		relay, err := f.liveness.heard(f.heartbeat(t, other, other, 10, tmtime.Now()))
		assert.ErrorIs(t, err, errProposerHeartbeatWrongProposer)
		assert.False(t, relay)
	})

	t.Run("should refuse a heartbeat forged in the proposer's name", func(t *testing.T) {
		relay, err := f.liveness.heard(f.heartbeat(t, other, proposer, 11, tmtime.Now()))
		assert.ErrorIs(t, err, types.ErrProposerHeartbeatInvalidSignature)
		assert.False(t, relay)
	})

	t.Run("should ignore a heartbeat signed too long ago", func(t *testing.T) {
		relay, err := f.liveness.heard(f.heartbeat(t, proposer, proposer, 12, tmtime.Now().Add(-2*time.Minute)))
		require.NoError(t, err)
		assert.False(t, relay)
	})

	t.Run("should ignore a heartbeat for another round", func(t *testing.T) {
		hb := f.heartbeat(t, proposer, proposer, 13, tmtime.Now())
		hb.Round = 1
		relay, err := f.liveness.heard(hb)
		require.NoError(t, err)
		assert.False(t, relay)
	})

	t.Run("should ignore heartbeats while nothing is watched", func(t *testing.T) {
		g := newLivenessFixture(t, nil)
		relay, err := g.liveness.heard(g.heartbeat(t, g.valSet.Validators[0], g.valSet.Validators[0], 1, tmtime.Now()))
		require.NoError(t, err)
		assert.False(t, relay)
	})
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
		f := newLivenessFixture(t, func(context.Context) error { return errors.New("application down") })
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
