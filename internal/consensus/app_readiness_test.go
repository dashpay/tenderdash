package consensus

import (
	"bufio"
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	abci "github.com/dashpay/tenderdash/abci/types"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/log"
)

func TestAppReadinessSendsOneProbeAtATime(t *testing.T) {
	var probes atomic.Int32
	answer := make(chan error)
	r := newAppReadiness(func(ctx context.Context) error {
		probes.Add(1)
		select {
		case err := <-answer:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	life, stop := context.WithCancel(context.Background())
	defer stop()

	// Callers that stop waiting leave the probe in flight, and the next ones
	// wait on it.
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		err := r.check(life, ctx)
		cancel()
		require.ErrorIs(t, err, context.DeadlineExceeded)
	}
	assert.EqualValues(t, 1, probes.Load(), "one probe is in flight")

	// The answer reaches the caller waiting when it comes.
	down := errors.New("application down")
	time.AfterFunc(20*time.Millisecond, func() { answer <- down })
	assert.ErrorIs(t, r.check(life, context.Background()), down)
	assert.EqualValues(t, 1, probes.Load(), "the caller waited on the probe in flight")

	// A probe that was answered is not reused.
	go func() { answer <- nil }()
	require.NoError(t, r.check(life, context.Background()))
	assert.EqualValues(t, 2, probes.Load())

	// The probe in flight ends with life.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	require.ErrorIs(t, r.check(life, ctx), context.DeadlineExceeded)
	cancel()
	stop()
	assert.Eventually(t, func() bool {
		r.mtx.Lock()
		defer r.mtx.Unlock()
		return r.inflight == nil
	}, time.Second, 5*time.Millisecond)
}

// A socket ABCI client keeps every request it sent until the application
// answers it. An application that reads requests and never answers gets one
// readiness probe in all, however many heartbeat intervals and rounds the
// proposer goes through.
func TestProposerReadinessKeepsOneSocketRequestUnanswered(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	var requests atomic.Int32
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		for {
			req := &abci.Request{}
			if err := abci.ReadMessage(reader, req); err != nil {
				return
			}
			requests.Add(1)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := abciclient.NewSocketClient(log.NewNopLogger(), "tcp://"+listener.Addr().String(), true)
	require.NoError(t, client.Start(ctx))
	defer client.Wait()
	defer cancel()

	blockExec := sm.NewBlockExecutor(nil, client, nil, nil, nil, nil)
	cs := &State{catchup: &catchupTracker{}, appReadiness: newAppReadiness(blockExec.AppReady)}
	f := newLivenessFixture(t, cs.proposerReady)
	proposer := f.valSet.Validators[0]
	pv := f.privValidator(t, proposer)

	// Each round the node proposes in starts a new heartbeat sender, which
	// probes every 10ms.
	for round := int32(0); round < 3; round++ {
		target := f.target(proposer, 50*time.Millisecond)
		target.round = round
		f.liveness.announce(ctx, target, pv)
		time.Sleep(100 * time.Millisecond)
		f.liveness.stop()
	}

	assert.Eventually(t, func() bool { return requests.Load() > 0 }, time.Second, 5*time.Millisecond)
	assert.EqualValues(t, 1, requests.Load(), "the application got one probe in all")
}
