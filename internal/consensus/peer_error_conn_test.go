package consensus

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/dash"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

// captureQueue records what the reactor hands to the peer side of the queue.
type captureQueue struct{ got []msgInfo }

func (q *captureQueue) send(_ context.Context, mi msgInfo) error {
	q.got = append(q.got, mi)
	return nil
}

func (q *captureQueue) recv(context.Context) (msgInfo, bool) { return msgInfo{}, false }

// The connection generation the router stamped on an envelope must reach the
// message the consensus goroutine processes, so a peer error raised for it can
// be fenced to that connection.
func TestLaneCtxCarriesConnIDIntoMsgInfo(t *testing.T) {
	const peerID = types.NodeID("sender")
	const g1 = uint64(7)
	ps := NewPeerState(log.NewNopLogger(), peerID)
	ps.SetLaneAdmission(g1, 1)
	r := &Reactor{}

	ctx, ok := r.laneCtx(context.Background(), ps, &p2p.Envelope{From: peerID, ConnID: g1})
	require.True(t, ok)
	q := &captureQueue{}
	sender := &chanMsgSender{peerQueue: q}
	require.NoError(t, sender.send(ctx, &CommitMessage{}, peerID))

	require.Len(t, q.got, 1)
	require.Equal(t, g1, q.got[0].ConnID)
}

// A forged commit's fatal report must name the connection that delivered it.
func TestCommitPeerErrorCarriesConnID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newCommitExtFixture(ctx, t)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
	f.node.ctrl.Get(TryAddCommitType).(*TryAddCommitAction).verificationBudget = &tokenBudget{tokens: 1_000}
	forged := *f.good
	forged.ThresholdBlockSignature = bytes.Clone(f.good.ThresholdBlockSignature)
	forged.ThresholdBlockSignature[0] ^= 0xff

	const g1 = uint64(7)
	f.node.msgInfoQueue.admitPeer("forger")
	env := msgEnvelope{msgInfo: msgInfo{Msg: &CommitMessage{Commit: &forged}, PeerID: "forger", ConnID: g1}}
	require.ErrorAs(t, commitMessageHandler(f.node.ctrl)(ctx, &f.stateData, env), &types.ErrInvalidCommitSignature{})

	report := <-f.node.peerErrorQueue.ch
	require.Equal(t, types.NodeID("forger"), report.PeerID)
	require.Equal(t, g1, report.ConnID, "the fatal report must be fenced to the sending connection")
}

// peerErrorRoutine must forward the originating generation to the router.
func TestPeerErrorRoutineForwardsConnID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const peerID = types.NodeID("forger")
	queue := &chanQueue[peerErrorMsg]{ch: make(chan peerErrorMsg, 1)}
	r := &Reactor{
		logger: log.NewNopLogger(),
		state:  &State{peerErrorQueue: queue},
		peers:  map[types.NodeID]*PeerState{peerID: NewPeerState(log.NewNopLogger(), peerID)},
	}
	errCh := make(chan p2p.PeerError, 1)
	go r.peerErrorRoutine(ctx, p2p.NewChannel(p2p.ConsensusStateChannel, "state", nil, nil, errCh))

	queue.ch <- peerErrorMsg{PeerID: peerID, ConnID: 7, Err: types.ErrInvalidCommitSignature{}, Fatal: true}
	select {
	case got := <-errCh:
		require.Equal(t, uint64(7), got.ConnID)
		require.True(t, got.Fatal)
	case <-time.After(10 * time.Second):
		t.Fatal("peer error was not forwarded")
	}
}
