package consensus

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// rejectRecoveryFixture holds a node at height 1 that has not received the block
// yet, a commit whose extension vector the application expects, and one it
// rejects. Both carry a valid threshold block signature.
type rejectRecoveryFixture struct {
	commitFixture
	stateData StateData
	checker   *commitCheckingExecutor
	good      *types.Commit
	bad       *types.Commit
}

func newRejectRecoveryFixture(ctx context.Context, t *testing.T) *rejectRecoveryFixture {
	t.Helper()
	cfg := configSetup(t)
	cfg.Consensus.DontAutoPropose = true
	n := newCommitFixture(ctx, t, cfg, types.BlockPartSizeBytes, 0)
	sd := n.node.GetStateData()
	ext := tmproto.VoteExtension{Type: tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW,
		Extension:      crypto.Checksum([]byte("withdrawal")),
		XSignRequestId: &tmproto.VoteExtension_SignRequestId{SignRequestId: []byte("withdrawal-request")}}
	votes := types.NewVoteSet(sd.state.ChainID, n.block.Height, 0, tmproto.PrecommitType, sd.Validators)
	good, err := factory.MakeCommit(ctx, n.commit.BlockID, n.block.Height, 0, votes, sd.Validators, n.privVals, ext)
	require.NoError(t, err)
	badValue := *good
	bad := &badValue
	bad.ThresholdVoteExtensions = nil
	require.NoError(t, sd.Validators.VerifyCommit(sd.state.ChainID, bad.BlockID, bad.Height, bad))
	expected, err := good.GetCanonicalVote()
	require.NoError(t, err)
	checker := &commitCheckingExecutor{Executor: n.node.blockExecutor.blockExec, t: t, block: n.block,
		round: 0, expected: expected.VoteExtensions.ToExtendProto(), store: n.node.blockStore}
	n.node.blockExecutor.blockExec = checker
	sd.updateRoundStep(0, cstypes.RoundStepPropose)
	return &rejectRecoveryFixture{commitFixture: n, stateData: sd, checker: checker, good: good, bad: bad}
}

type commitSend struct {
	commit *types.Commit
	peer   types.NodeID
}

func (f *rejectRecoveryFixture) sendCommit(ctx context.Context, t *testing.T, commit *types.Commit, peer types.NodeID) {
	t.Helper()
	f.dispatchCommit(ctx, t, commit, peer, false)
}

func (f *rejectRecoveryFixture) dispatchCommit(ctx context.Context, t *testing.T, commit *types.Commit, peer types.NodeID, fromReplay bool) {
	t.Helper()
	commitCtx := msgInfoWithCtx(ctx, msgInfo{Msg: &CommitMessage{commit}, PeerID: peer})
	require.NoError(t, f.node.ctrl.Dispatch(commitCtx,
		&TryAddCommitEvent{Commit: commit, PeerID: peer, FromReplay: fromReplay}, &f.stateData))
}

// completeBlock delivers the block's only part and returns the dispatch error.
func (f *rejectRecoveryFixture) completeBlock(ctx context.Context) error {
	msg := &BlockPartMessage{Height: f.block.Height, Round: 0, Part: f.parts.GetPart(0)}
	partCtx := msgInfoWithCtx(ctx, msgInfo{Msg: msg, PeerID: f.peerID})
	return f.node.ctrl.Dispatch(partCtx, &AddProposalBlockPartEvent{Msg: msg, PeerID: f.peerID}, &f.stateData)
}

func (f *rejectRecoveryFixture) requireCommitted(t *testing.T, want *types.Commit) {
	t.Helper()
	require.Equal(t, f.block.Height+1, f.stateData.Height, "the height must complete without a restart")
	require.Equal(t, f.block.Height, f.node.blockStore.Height())
	seen := f.node.blockStore.LoadSeenCommitAt(f.block.Height)
	require.NotNil(t, seen)
	require.Equal(t, want.ThresholdVoteExtensions, seen.ThresholdVoteExtensions,
		"only the accepted extension vector may be persisted")
}

// An honest commit gossiped while an altered one is parked is not resent: the
// sender has already marked this node as holding a commit. It has to be retried
// once the parked commit is rejected, or the height stalls until a restart.
func TestCommitRejectRetriesCommitReceivedWhileParked(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sends    func(f *rejectRecoveryFixture) []commitSend
		verifies int
	}{
		{
			name: "honest after attacker",
			sends: func(f *rejectRecoveryFixture) []commitSend {
				return []commitSend{{f.bad, "attacker"}, {f.good, "honest"}}
			},
			verifies: 2,
		},
		{
			name: "attacker resends after honest",
			sends: func(f *rejectRecoveryFixture) []commitSend {
				return []commitSend{{f.bad, "attacker"}, {f.good, "honest"}, {f.bad, "attacker"}, {f.bad, "attacker"}}
			},
			// The attacker's resends fill a slot behind the honest one and cannot
			// displace it, so the honest commit is the first replacement tried.
			verifies: 2,
		},
		{
			name: "several attackers ahead of the honest peer",
			sends: func(f *rejectRecoveryFixture) []commitSend {
				return []commitSend{{f.bad, "attacker-1"}, {f.bad, "attacker-2"}, {f.bad, "attacker-3"}, {f.good, "honest"}}
			},
			verifies: 4,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := newRejectRecoveryFixture(ctx, t)
			ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)

			for _, send := range tc.sends(f) {
				f.sendCommit(ctx, t, send.commit, send.peer)
			}
			require.Same(t, f.bad, f.stateData.Commit, "the first commit is parked until its block arrives")
			require.Empty(t, f.checker.calls, "nothing can be verified before the block is processed")

			require.NoError(t, f.completeBlock(ctx), "the rejected commit must be handled without blaming the block-part peer")

			f.requireCommitted(t, f.good)
			want := make([]string, 0, tc.verifies+2)
			want = append(want, "process")
			for range tc.verifies {
				want = append(want, "verify")
			}
			require.Equal(t, append(want, "finalize"), f.checker.calls)
		})
	}
}

// WAL replay re-dispatches the same messages in the same order, so it must
// reach the same outcome, and without charging any peer for what it replays.
func TestCommitRejectRetriesCommitsDuringReplay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newRejectRecoveryFixture(ctx, t)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
	// A replacement with a forged block signature is one only a dishonest
	// peer can send, and is reported when it arrives live.
	forged := *f.good
	forged.ThresholdBlockSignature = bytes.Clone(f.good.ThresholdBlockSignature)
	forged.ThresholdBlockSignature[0] ^= 0xff

	f.dispatchCommit(ctx, t, f.bad, "attacker", true)
	f.dispatchCommit(ctx, t, &forged, "forger", true)
	f.dispatchCommit(ctx, t, f.good, "honest", true)
	msg := &BlockPartMessage{Height: f.block.Height, Round: 0, Part: f.parts.GetPart(0)}
	partCtx := msgInfoWithCtx(ctx, msgInfo{Msg: msg, PeerID: f.peerID})
	require.NoError(t, f.node.ctrl.Dispatch(partCtx,
		&AddProposalBlockPartEvent{Msg: msg, PeerID: f.peerID, FromReplay: true}, &f.stateData))

	f.requireCommitted(t, f.good)
	require.Equal(t, []string{"process", "verify", "verify", "finalize"}, f.checker.calls)
	select {
	case report := <-f.node.peerErrorQueue.ch:
		t.Fatalf("a replayed commit must not be reported: %v", report)
	default:
	}
}

// A validator can hold its own +2/3 precommits for the block while a commit
// from a peer is parked. The parked commit takes the block when it completes;
// rejected, it must hand the height back to the quorum already held.
func TestCommitRejectFallsBackToOwnPrecommitQuorum(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newRejectRecoveryFixture(ctx, t)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
	// Local precommits carry no extensions, so the quorum's vector is empty; the
	// peer's commit carries one the application did not produce.
	f.checker.expected = []*abci.ExtendVoteExtension{}
	peerCommit := f.good

	f.sendCommit(ctx, t, peerCommit, "attacker")
	require.Same(t, peerCommit, f.stateData.Commit)
	f.stateData.updateRoundStep(0, cstypes.RoundStepPrecommit)
	f.deliver(ctx, t, &f.stateData, f.precommit(ctx, t, f.commit.BlockID))
	require.Equal(t, cstypes.RoundStepApplyCommit, f.stateData.Step, "the quorum waits for the block")

	require.NoError(t, f.completeBlock(ctx))

	require.Equal(t, f.block.Height+1, f.stateData.Height, "the held quorum must complete the height")
	require.Equal(t, f.block.Height, f.node.blockStore.Height())
	require.Empty(t, f.node.blockStore.LoadSeenCommitAt(f.block.Height).ThresholdVoteExtensions)
	require.Equal(t, []string{"process", "verify", "verify", "finalize"}, f.checker.calls)
}

// The commit built from this node's own quorum goes through tryFinalizeCommit.
// A rejection there must be reported, leave nothing persisted, and move the node
// on to a round in which a commit from a peer can still finish the height.
func TestLocalCommitRejectedInTryFinalizeCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newRejectRecoveryFixture(ctx, t)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
	f.checker.expected = []*abci.ExtendVoteExtension{}
	f.checker.rejectAll = true

	f.stateData.updateRoundStep(0, cstypes.RoundStepPrecommit)
	f.deliver(ctx, t, &f.stateData, f.precommit(ctx, t, f.commit.BlockID))
	require.Equal(t, cstypes.RoundStepApplyCommit, f.stateData.Step, "the quorum waits for the block")
	// The completing part finalizes the quorum's commit through tryFinalizeCommit.
	require.NoError(t, f.completeBlock(ctx))

	require.Equal(t, []string{"process", "verify"}, f.checker.calls, "the rejected quorum is not assembled again")
	require.Zero(t, f.node.blockStore.Height())
	require.Equal(t, f.block.Height, f.stateData.Height)
	require.Nil(t, f.stateData.Commit)
	require.Less(t, f.stateData.Step, cstypes.RoundStepApplyCommit)
	persisted := f.node.GetStateData()
	require.Nil(t, persisted.Commit)
	require.Less(t, persisted.Step, cstypes.RoundStepApplyCommit)

	require.Equal(t, int32(1), f.stateData.Round)

	f.checker.rejectAll = false
	f.sendCommit(ctx, t, f.commit, "honest")
	require.NoError(t, f.completeBlock(ctx))
	require.Equal(t, f.block.Height+1, f.stateData.Height)
	require.Equal(t, f.block.Height, f.node.blockStore.Height())
}

// With nothing to replace a rejected commit, the node must not sit in a step
// whose timeouts have all fired. It moves to the next round, which makes a peer
// that already sent a commit send it again, and that commit finishes the height.
func TestCommitRejectWithoutReplacementMovesToNextRound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newRejectRecoveryFixture(ctx, t)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)

	// An honest peer's view of this node, which it updates from our round steps.
	peer := NewPeerState(log.NewNopLogger(), "honest")
	peer.PRS.Height, peer.PRS.Round, peer.PRS.Step = f.stateData.Height, f.stateData.Round, f.stateData.Step
	f.node.emitter.AddListener(types.EventNewRoundStepValue, func(data eventemitter.EventData) error {
		msg, err := MsgFromProto(data.(*cstypes.RoundState).NewRoundStepMessage())
		require.NoError(t, err)
		peer.ApplyNewRoundStepMessage(msg.(*NewRoundStepMessage))
		return nil
	})
	peerRS := cstypes.RoundState{Height: f.block.Height + 1}

	f.sendCommit(ctx, t, f.bad, "attacker")
	peer.SetHasCommit(f.good) // sent, but lost to a verification budget or a restart
	require.NoError(t, f.completeBlock(ctx))
	require.Equal(t, f.block.Height, f.stateData.Height)
	require.Equal(t, int32(1), f.stateData.Round)
	require.True(t, shouldCommitBeGossiped(peerRS, peer.GetRoundState()), "the new round makes the peer resend")

	require.True(t, f.stateData.holdsProposalBlock(f.good.BlockID), "keep the verified block across recovery")
	f.sendCommit(ctx, t, f.good, "honest")
	require.Equal(t, []string{"process", "verify", "verify", "finalize"}, f.checker.calls)
	f.requireCommitted(t, f.good)
}

// Only an accepted commit may be announced to peers. A rejected one, whether
// parked or retried as a replacement, must never be relayed.
func TestRejectedCommitsAreNotRelayed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newRejectRecoveryFixture(ctx, t)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
	var relayed []*types.Commit
	f.node.emitter.AddListener(types.EventCommitValue, func(data eventemitter.EventData) error {
		relayed = append(relayed, data.(*types.Commit))
		return nil
	})
	duplicated := *f.good
	duplicated.ThresholdVoteExtensions = append(duplicated.ThresholdVoteExtensions, f.good.ThresholdVoteExtensions...)

	f.sendCommit(ctx, t, f.bad, "attacker-1")
	f.sendCommit(ctx, t, &duplicated, "attacker-2")
	f.sendCommit(ctx, t, f.good, "honest")
	require.NoError(t, f.completeBlock(ctx))

	f.requireCommitted(t, f.good)
	require.Equal(t, []*types.Commit{f.good}, relayed)
}

// recordTimeouts captures the timeouts the node schedules from now on.
func (f *rejectRecoveryFixture) recordTimeouts() *recordingTicker {
	ticker := &recordingTicker{}
	f.node.roundScheduler.timeoutTicker = ticker
	return ticker
}

type recordingTicker struct {
	TimeoutTicker
	scheduled []timeoutInfo
}

func (r *recordingTicker) ScheduleTimeout(ti timeoutInfo) {
	r.scheduled = append(r.scheduled, ti)
}

func TestCommitRejectAfterPrecommitTimeoutStillAdvancesRound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newRejectRecoveryFixture(ctx, t)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
	ticker := f.recordTimeouts()
	ticker.scheduled = append(ticker.scheduled, timeoutInfo{
		Height: f.block.Height, Round: 0, Step: cstypes.RoundStepPrecommitWait,
	})
	f.sendCommit(ctx, t, f.bad, "attacker")
	require.NoError(t, f.completeBlock(ctx))
	require.Equal(t, int32(1), f.stateData.Round)
	for _, ti := range ticker.scheduled[1:] {
		require.False(t, ti.Round == 0 && ti.Step == cstypes.RoundStepPrecommitWait,
			"recovery must not re-arm an already consumed timeout step")
	}
	f.sendCommit(ctx, t, f.good, "honest")
	require.NoError(t, f.completeBlock(ctx))
	f.requireCommitted(t, f.good)
}

func TestCommitExtensionRejectionRecordsMetric(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newRejectRecoveryFixture(ctx, t)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
	counter := &recordingCounter{}
	f.node.metrics.CommitVerifyFailures = counter
	f.sendCommit(ctx, t, f.bad, "attacker")
	require.NoError(t, f.completeBlock(ctx))
	require.Equal(t, float64(1), counter.value)
}
