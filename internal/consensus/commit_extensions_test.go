package consensus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// commitExtFixture holds a node at height 1 that has not received the block
// yet, a commit whose extension vector the application expects, and one it
// rejects. Both carry a valid threshold block signature.
type commitExtFixture struct {
	commitFixture
	stateData StateData
	checker   *commitCheckingExecutor
	good      *types.Commit
	bad       *types.Commit
}

var withdrawalExtension = tmproto.VoteExtension{Type: tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW,
	Extension:      crypto.Checksum([]byte("withdrawal")),
	XSignRequestId: &tmproto.VoteExtension_SignRequestId{SignRequestId: []byte("withdrawal-request")}}

// otherExtension is a vector the application does not expect.
var otherExtension = tmproto.VoteExtension{Type: tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW,
	Extension:      crypto.Checksum([]byte("other")),
	XSignRequestId: &tmproto.VoteExtension_SignRequestId{SignRequestId: []byte("other-request")}}

func newCommitExtFixture(ctx context.Context, t *testing.T) *commitExtFixture {
	t.Helper()
	n := newCommitFixture(ctx, t, configSetup(t), types.BlockPartSizeBytes, 0)
	f := &commitExtFixture{commitFixture: n, stateData: n.node.GetStateData()}
	f.good = f.sign(ctx, t, 0, withdrawalExtension)
	badValue := *f.good
	f.bad = &badValue
	f.bad.ThresholdVoteExtensions = nil
	expected, err := f.good.GetCanonicalVote()
	require.NoError(t, err)
	f.checker = &commitCheckingExecutor{Executor: n.node.blockExecutor.blockExec, t: t, block: n.block,
		round: 0, expected: expected.VoteExtensions.ToExtendProto(), store: n.node.blockStore}
	n.node.blockExecutor.blockExec = f.checker
	f.stateData.updateRoundStep(0, cstypes.RoundStepPropose)
	return f
}

// sign returns a commit for the fixture's block at round, carrying extension.
func (f *commitExtFixture) sign(ctx context.Context, t *testing.T, round int32, extensions ...tmproto.VoteExtension) *types.Commit {
	t.Helper()
	return f.signAt(ctx, t, f.block.Height, round, extensions...)
}

func (f *commitExtFixture) signAt(ctx context.Context, t *testing.T, height int64, round int32,
	extensions ...tmproto.VoteExtension) *types.Commit {
	t.Helper()
	sd := f.stateData
	votes := types.NewVoteSet(sd.state.ChainID, height, round, tmproto.PrecommitType, sd.Validators)
	commit, err := factory.MakeCommit(ctx, f.commit.BlockID, height, round, votes, sd.Validators, f.privVals, extensions...)
	require.NoError(t, err)
	return commit
}

// sendCommit delivers commit from a connected peer and returns the dispatch error.
func (f *commitExtFixture) sendCommit(ctx context.Context, commit *types.Commit, peer types.NodeID) error {
	f.node.msgInfoQueue.admitPeer(peer)
	return f.dispatchCommit(ctx, commit, peer, false)
}

func (f *commitExtFixture) dispatchCommit(ctx context.Context, commit *types.Commit, peer types.NodeID, fromReplay bool) error {
	commitCtx := msgInfoWithCtx(ctx, msgInfo{Msg: &CommitMessage{commit}, PeerID: peer})
	return f.node.ctrl.Dispatch(commitCtx,
		&TryAddCommitEvent{Commit: commit, PeerID: peer, FromReplay: fromReplay}, &f.stateData)
}

// completeBlock delivers the block's only part and returns the dispatch error.
func (f *commitExtFixture) completeBlock(ctx context.Context, fromReplay bool) error {
	msg := &BlockPartMessage{Height: f.block.Height, Round: 0, Part: f.parts.GetPart(0)}
	partCtx := msgInfoWithCtx(ctx, msgInfo{Msg: msg, PeerID: f.peerID})
	return f.node.ctrl.Dispatch(partCtx,
		&AddProposalBlockPartEvent{Msg: msg, PeerID: f.peerID, FromReplay: fromReplay}, &f.stateData)
}

// holdBlock gives the round the processed block, as if its parts had arrived.
func (f *commitExtFixture) holdBlock(ctx context.Context, t *testing.T) {
	t.Helper()
	f.stateData.ProposalBlock, f.stateData.ProposalBlockParts = f.block, f.parts
	require.NoError(t, f.node.blockExecutor.ensureProcess(ctx, &f.stateData.RoundState, 0))
	f.checker.calls = nil
}

func (f *commitExtFixture) requireCommitted(t *testing.T, want *types.Commit) {
	t.Helper()
	require.Equal(t, f.block.Height+1, f.stateData.Height, "the height must complete without a restart")
	require.Equal(t, f.block.Height, f.node.blockStore.Height())
	seen := f.node.blockStore.LoadSeenCommitAt(f.block.Height)
	require.NotNil(t, seen)
	require.Equal(t, want.ThresholdVoteExtensions, seen.ThresholdVoteExtensions,
		"only the accepted extension vector may be persisted")
}

// requireNothingPersisted checks that a rejected commit left no trace a restart
// could resume from.
func (f *commitExtFixture) requireNothingPersisted(t *testing.T) {
	t.Helper()
	require.Zero(t, f.node.blockStore.Height())
	require.Equal(t, f.block.Height, f.stateData.Height)
	require.Nil(t, f.stateData.Commit, "a rejected commit must not block a replacement")
	persisted := f.node.GetStateData()
	require.Nil(t, persisted.Commit)
	require.Less(t, persisted.Step, cstypes.RoundStepApplyCommit)
	require.Equal(t, int32(-1), persisted.CommitRound)
	require.True(t, persisted.CommitTime.IsZero())
}

func calls(verifies int, tail ...string) []string {
	out := []string{"process"}
	for range verifies {
		out = append(out, "verify")
	}
	return append(out, tail...)
}

// An authentic commit whose extension vector the application rejects is refused
// before anything is saved, whichever way it reaches the node; the next
// commit with the right vector then finishes the height.
func TestCommitExtensionsRejectedBeforeSave(t *testing.T) {
	for _, path := range []string{"held", "parked", "future", "future held", "replay"} {
		for _, mutation := range []string{"strip", "duplicate", "cross-height replay", "empty"} {
			t.Run(path+"/"+mutation, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				f := newCommitExtFixture(ctx, t)
				ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
				round := int32(0)
				if path == "future" || path == "future held" {
					round = 2
					f.checker.round = round
				}
				good := f.sign(ctx, t, round, withdrawalExtension)
				badValue := *good
				bad := &badValue
				switch mutation {
				case "strip":
					bad.ThresholdVoteExtensions = nil
				case "duplicate":
					bad.ThresholdVoteExtensions = append(bad.ThresholdVoteExtensions, bad.ThresholdVoteExtensions[0])
				case "cross-height replay":
					old := withdrawalExtension
					old.Extension = crypto.Checksum([]byte("previous withdrawal"))
					old.XSignRequestId = &tmproto.VoteExtension_SignRequestId{SignRequestId: []byte("previous request")}
					bad.ThresholdVoteExtensions = f.signAt(ctx, t, f.block.Height+10, round, old).ThresholdVoteExtensions
				case "empty":
					bad.ThresholdVoteExtensions = tmproto.VoteExtensions{}
				}
				sd := &f.stateData
				require.NoError(t, sd.Validators.VerifyCommit(sd.state.ChainID, bad.BlockID, bad.Height, bad))
				held := path == "held" || path == "future held"
				f.checker.onVerify = func(*types.Vote) {
					if path == "held" {
						require.Nil(t, sd.Commit, "an unverified commit must not be published in the round state")
					}
				}
				f.checker.onFinalize = func(commit *types.Commit) {
					require.Same(t, commit, sd.Commit, "the accepted commit must be published in the round state")
				}
				if held {
					f.holdBlock(ctx, t)
				}
				var relayed []*types.Commit
				f.node.emitter.AddListener(types.EventCommitValue, func(data eventemitter.EventData) error {
					relayed = append(relayed, data.(*types.Commit))
					return nil
				})
				counter := &recordingCounter{}
				f.node.metrics.CommitVerifyFailures = counter

				const sender = types.NodeID("sender")
				f.node.msgInfoQueue.admitPeer(sender)
				err := f.dispatchCommit(ctx, bad, sender, path == "replay")
				if held {
					require.ErrorIs(t, err, sm.ErrCommitExtensionsRejected)
					require.Equal(t, []string{"verify"}, f.checker.calls[len(f.checker.calls)-1:])
				} else {
					require.NoError(t, err)
					require.Empty(t, f.checker.calls, "cannot check until the block is processed")
					require.NoError(t, f.completeBlock(ctx, path == "replay"))
					require.Equal(t, calls(1), f.checker.calls)
				}
				f.requireNothingPersisted(t)
				require.Empty(t, relayed, "a rejected commit must never be relayed")
				require.Equal(t, float64(1), counter.value)
				if path == "future held" {
					require.Zero(t, sd.Round, "a rejected commit must not change the round")
				}
				select {
				case report := <-f.node.peerErrorQueue.ch:
					t.Fatalf("an application rejection must not evict the sender: %v", report)
				default:
				}

				f.checker.calls = nil
				require.NoError(t, f.dispatchCommit(ctx, good, sender, false))
				f.requireCommitted(t, good)
				require.Equal(t, []string{"verify", "finalize"}, f.checker.calls, "the processed block is kept")
				require.Equal(t, []*types.Commit{good}, relayed)
			})
		}
	}
}

// A peer resending an authentic commit with stripped extensions must not make
// this node change rounds: every round retains vote sets and lengthens the
// timeouts, so a rejection that advanced the round would let a single peer grow
// both without bound.
func TestRepeatedRejectedCommitKeepsRound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newCommitExtFixture(ctx, t)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
	future := f.sign(ctx, t, 2, withdrawalExtension)
	future.ThresholdVoteExtensions = nil
	f.checker.round = -1
	f.holdBlock(ctx, t)
	round, voteRound := f.stateData.Round, f.stateData.Votes.Round()

	const resends = 50
	for range resends {
		require.ErrorIs(t, f.sendCommit(ctx, f.bad, "attacker"), sm.ErrCommitExtensionsRejected)
		require.ErrorIs(t, f.sendCommit(ctx, future, "attacker"), sm.ErrCommitExtensionsRejected)
	}

	require.Equal(t, f.block.Height, f.stateData.Height)
	require.Equal(t, round, f.stateData.Round, "a rejected commit must not advance the round")
	require.Equal(t, voteRound, f.stateData.Votes.Round(), "a rejected commit must not add vote sets")
	require.Nil(t, f.stateData.Votes.Prevotes(round+1))
	require.Equal(t, cstypes.RoundStepPropose, f.stateData.Step, "the round keeps its step and its timeout")

	f.checker.calls = nil
	require.NoError(t, f.sendCommit(ctx, f.good, "honest"))
	f.requireCommitted(t, f.good)
	// The later round's commits had the block processed for their round.
	require.Equal(t, calls(1, "finalize"), f.checker.calls)
}

// A rejection keeps the processed block and its ProcessProposal result, and
// creates no proposal and schedules no timeout, whether or not this node
// proposes the round.
func TestCommitRejectKeepsProcessedBlock(t *testing.T) {
	for _, proposer := range []bool{false, true} {
		t.Run(fmt.Sprintf("proposer=%v", proposer), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := newCommitExtFixture(ctx, t)
			require.False(t, f.node.config.DontAutoPropose)
			ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
			round := int32(0)
			for {
				next, err := f.stateData.ProposerSelector.GetProposer(f.stateData.Height, round)
				require.NoError(t, err)
				if bytes.Equal(next.ProTxHash, f.node.privValidator.ProTxHash) == proposer {
					break
				}
				round++
			}
			f.holdBlock(ctx, t)
			processed := f.stateData.CurrentRoundState
			creator := enterProposeWithCountingCreator(f.node)
			ticker := &recordingTicker{}
			f.node.roundScheduler.timeoutTicker = ticker
			f.stateData.updateRoundStep(round, cstypes.RoundStepPropose)

			require.ErrorIs(t, f.sendCommit(ctx, f.bad, "attacker"), sm.ErrCommitExtensionsRejected)

			require.Equal(t, round, f.stateData.Round)
			require.Equal(t, cstypes.RoundStepPropose, f.stateData.Step)
			require.True(t, f.stateData.holdsProposalBlock(f.good.BlockID))
			require.Equal(t, processed, f.stateData.CurrentRoundState)
			require.Zero(t, creator.calls.Load(), "a rejection must not create another proposal")
			require.Empty(t, ticker.scheduled, "a rejection schedules nothing; the round's own timeout stands")
			require.NoError(t, f.sendCommit(ctx, f.good, "honest"))
			f.requireCommitted(t, f.good)
			require.Equal(t, []string{"verify", "verify", "finalize"}, f.checker.calls)
		})
	}
}

type recordingTicker struct {
	TimeoutTicker
	scheduled []timeoutInfo
}

func (r *recordingTicker) ScheduleTimeout(ti timeoutInfo) {
	r.scheduled = append(r.scheduled, ti)
}

// An honest commit gossiped while an altered one is parked is not resent: the
// sender has already marked this node as holding a commit. It has to be tried
// once the parked commit is rejected, or the height stalls until a restart; no
// number of other peers can push it out.
func TestParkedCommitRejectTriesQueuedCommits(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sends    func(f *commitExtFixture) []commitSend
		verifies int
	}{
		{
			name: "honest after attacker",
			sends: func(f *commitExtFixture) []commitSend {
				return []commitSend{{f.bad, "attacker"}, {f.good, "honest"}}
			},
			verifies: 2,
		},
		{
			name: "attacker resends after honest",
			sends: func(f *commitExtFixture) []commitSend {
				return []commitSend{{f.bad, "attacker"}, {f.good, "honest"}, {f.bad, "attacker"}, {f.bad, "attacker"}}
			},
			// The attacker's resends equal the parked commit and cannot displace
			// the honest one, which is the first replacement tried.
			verifies: 2,
		},
		{
			name: "more attackers than any fixed cap",
			sends: func(f *commitExtFixture) []commitSend {
				sends := []commitSend{{f.bad, "attacker"}}
				for i := range 40 {
					sends = append(sends, commitSend{f.bad, types.NodeID(fmt.Sprintf("sybil-%d", i))})
				}
				return append(sends, commitSend{f.good, "honest"})
			},
			// Copies of the parked commit add nothing to try and take no slot.
			verifies: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := newCommitExtFixture(ctx, t)
			ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
			var relayed []*types.Commit
			f.node.emitter.AddListener(types.EventCommitValue, func(data eventemitter.EventData) error {
				relayed = append(relayed, data.(*types.Commit))
				return nil
			})

			for _, send := range tc.sends(f) {
				require.NoError(t, f.sendCommit(ctx, send.commit, send.peer))
			}
			require.Same(t, f.bad, f.stateData.Commit, "the first commit is parked until its block arrives")
			require.Empty(t, f.checker.calls, "nothing can be verified before the block is processed")

			require.NoError(t, f.completeBlock(ctx, false), "a rejection must not blame the block-part peer")

			f.requireCommitted(t, f.good)
			require.Equal(t, calls(tc.verifies, "finalize"), f.checker.calls)
			require.Equal(t, []*types.Commit{f.good}, relayed, "only the accepted commit is relayed")
		})
	}
}

type commitSend struct {
	commit *types.Commit
	peer   types.NodeID
}

// Queued commits pay for their authentication when they arrive, in their own
// scheduler turn, and a forged one is reported then. Trying them once the block
// arrives costs the budget nothing: that charge would be out of turn.
func TestQueuedCommitsAreAuthenticatedOnArrival(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newCommitExtFixture(ctx, t)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
	budget := &tokenBudget{tokens: 1_000}
	f.node.ctrl.Get(TryAddCommitType).(*TryAddCommitAction).verificationBudget = budget
	forged := *f.good
	forged.ThresholdBlockSignature = bytes.Clone(f.good.ThresholdBlockSignature)
	forged.ThresholdBlockSignature[0] ^= 0xff

	require.NoError(t, f.sendCommit(ctx, f.bad, "attacker"))
	require.ErrorAs(t, f.sendCommit(ctx, &forged, "forger"), &types.ErrInvalidCommitSignature{})
	report := <-f.node.peerErrorQueue.ch
	require.Equal(t, types.NodeID("forger"), report.PeerID)
	charged := budget.charged
	require.NoError(t, f.sendCommit(ctx, f.good, "honest"))
	cost, err := commitCost(len(f.good.ThresholdVoteExtensions))
	require.NoError(t, err)
	require.Equal(t, cost, budget.charged-charged, "a queued commit pays in its own turn")

	budget.tokens = 0
	require.NoError(t, f.completeBlock(ctx, false))
	f.requireCommitted(t, f.good)
	require.Equal(t, calls(2, "finalize"), f.checker.calls, "the forged commit is never tried")
}

// tokenBudget is a verification budget holding a fixed number of tokens that
// never refill, so a test decides exactly which verifications it can afford.
type tokenBudget struct {
	tokens  int
	charged int
}

func (b *tokenBudget) Allow(cost int) bool {
	if cost > b.tokens {
		return false
	}
	b.tokens -= cost
	b.charged += cost
	return true
}

// WAL replay re-dispatches the same messages in the same order, so it must
// reach the same outcome, without reporting any peer for what it replays.
func TestParkedCommitRejectDuringReplay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newCommitExtFixture(ctx, t)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
	forged := *f.good
	forged.ThresholdBlockSignature = bytes.Clone(f.good.ThresholdBlockSignature)
	forged.ThresholdBlockSignature[0] ^= 0xff

	require.NoError(t, f.dispatchCommit(ctx, f.bad, "attacker", true))
	require.Error(t, f.dispatchCommit(ctx, &forged, "forger", true))
	require.NoError(t, f.dispatchCommit(ctx, f.good, "honest", true))
	require.NoError(t, f.completeBlock(ctx, true))

	f.requireCommitted(t, f.good)
	require.Equal(t, calls(2, "finalize"), f.checker.calls)
	select {
	case report := <-f.node.peerErrorQueue.ch:
		t.Fatalf("a replayed commit must not be reported: %v", report)
	default:
	}
}

// A commit for a later round queued behind a rejected parked one is applied
// without entering its round.
func TestParkedCommitRejectAppliesQueuedFutureRound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newCommitExtFixture(ctx, t)
	const round int32 = 2
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
	future := f.sign(ctx, t, round, withdrawalExtension)
	f.checker.onVerify = func(vote *types.Vote) {
		if vote.Round == 0 {
			f.checker.round = round
		}
	}
	require.NoError(t, f.sendCommit(ctx, f.bad, "attacker"))
	require.NoError(t, f.sendCommit(ctx, future, "honest"))
	require.NoError(t, f.completeBlock(ctx, false))
	f.requireCommitted(t, future)
	require.Equal(t, []string{"process", "verify", "process", "verify", "finalize"}, f.checker.calls)
}

// When every parked and queued commit is rejected, nothing more is tried and
// the completed block takes the ordinary path, which a later honest commit
// finishes. An honest peer that has not sent its commit yet still sends it.
func TestParkedCommitRejectWithoutReplacement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newCommitExtFixture(ctx, t)
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

	require.NoError(t, f.sendCommit(ctx, f.bad, "attacker"))
	require.NoError(t, f.sendCommit(ctx, f.bad, "attacker-2"))
	require.NoError(t, f.completeBlock(ctx, false))
	f.requireNothingPersisted(t)
	require.Zero(t, f.stateData.Round)
	require.Equal(t, cstypes.RoundStepPropose, f.stateData.Step, "a rejection leaves the round's step and timeout")
	require.True(t, shouldCommitBeGossiped(peerRS, peer.GetRoundState()), "a peer that has not sent yet still sends")

	require.True(t, f.stateData.holdsProposalBlock(f.good.BlockID), "the verified block is kept")
	require.NoError(t, f.sendCommit(ctx, f.good, "honest"))
	require.Equal(t, calls(2, "finalize"), f.checker.calls, "the copy of the parked commit is not tried")
	f.requireCommitted(t, f.good)
}

// A validator can hold its own +2/3 precommits for the block while a commit
// from a peer is parked. The parked commit takes the block when it completes;
// rejected, the quorum already held finishes the height.
func TestParkedCommitRejectFallsBackToOwnPrecommits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newCommitExtFixture(ctx, t)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
	// Local precommits carry no extensions, so the quorum's vector is empty; the
	// peer's commit carries one the application did not produce.
	f.checker.expected = []*abci.ExtendVoteExtension{}

	require.NoError(t, f.sendCommit(ctx, f.good, "attacker"))
	f.stateData.updateRoundStep(0, cstypes.RoundStepPrecommit)
	f.deliver(ctx, t, &f.stateData, f.precommit(ctx, t, f.commit.BlockID))
	require.Equal(t, cstypes.RoundStepApplyCommit, f.stateData.Step, "the quorum waits for the block")

	require.NoError(t, f.completeBlock(ctx, false))

	require.Equal(t, f.block.Height+1, f.stateData.Height, "the held quorum must complete the height")
	require.Empty(t, f.node.blockStore.LoadSeenCommitAt(f.block.Height).ThresholdVoteExtensions)
	require.Equal(t, calls(2, "finalize"), f.checker.calls)
}

// The commit built from this node's own quorum is checked in tryFinalizeCommit.
// A rejection there persists nothing and leaves the node at its height, where
// a peer's commit carrying a vector the application accepts can still finish it.
// The same commit from a peer needs no ProcessProposal, so the application
// judges it again.
func TestOwnPrecommitsCommitRejected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newCommitExtFixture(ctx, t)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
	counter := &recordingCounter{}
	f.node.metrics.CommitVerifyFailures = counter
	var logged bytes.Buffer
	logger, err := log.NewLogger("debug", &logged)
	require.NoError(t, err)
	f.node.ctrl.Get(TryFinalizeCommitType).(*TryFinalizeCommitAction).logger = logger

	// Local precommits carry no extensions; the application expects the
	// withdrawal vector.
	f.stateData.updateRoundStep(0, cstypes.RoundStepPrecommit)
	f.deliver(ctx, t, &f.stateData, f.precommit(ctx, t, f.commit.BlockID))
	require.Equal(t, cstypes.RoundStepApplyCommit, f.stateData.Step, "the quorum waits for the block")
	require.NoError(t, f.completeBlock(ctx, false))

	require.Equal(t, calls(1), f.checker.calls)
	require.Equal(t, float64(1), counter.value)
	require.Contains(t, logged.String(), `"level":"error"`,
		"the rejection of this node's own commit is a local fault and logs at Error")
	require.Zero(t, f.node.blockStore.Height())
	require.Equal(t, f.block.Height, f.stateData.Height)
	require.Nil(t, f.stateData.Commit)

	require.ErrorIs(t, f.sendCommit(ctx, f.commit, "relayer"), sm.ErrCommitExtensionsRejected)
	require.Equal(t, calls(2), f.checker.calls, "the block is not processed again")

	require.NoError(t, f.sendCommit(ctx, f.good, "honest"))
	f.requireCommitted(t, f.good)
	require.Equal(t, calls(3, "finalize"), f.checker.calls)
}

func TestAcceptedCommitDiscardsQueuedCandidates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newCommitExtFixture(ctx, t)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
	require.NoError(t, f.sendCommit(ctx, f.good, "honest"))
	require.NoError(t, f.sendCommit(ctx, f.bad, "attacker"))
	candidates := f.node.ctrl.Get(TryAddCommitType).(*TryAddCommitAction).candidates
	require.Len(t, candidates.candidates, 1)
	require.NoError(t, f.completeBlock(ctx, false))
	f.requireCommitted(t, f.good)
	require.Empty(t, candidates.candidates, "completed heights must release queued commits")
	require.Equal(t, calls(1, "finalize"), f.checker.calls, "the queued commit is not checked")
}

// Honest commits for one block can differ in round alone, so the commits queued
// behind a parked one may alternate between rounds. They are tried a round at a
// time, so the block is processed once per round rather than once per switch.
func TestParkedCommitsAreTriedRoundByRound(t *testing.T) {
	for _, tc := range []struct {
		name        string
		honestRound int32
		// processedFirst has the block processed for round 1 before it
		// completes, as a node that already holds its result would.
		processedFirst bool
		rounds         []int32
	}{
		{name: "honest in the parked round", honestRound: 0, rounds: []int32{0}},
		{name: "honest in a later round", honestRound: 1, rounds: []int32{0, 1}},
		{name: "honest in the processed round", honestRound: 1, processedFirst: true, rounds: []int32{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := newCommitExtFixture(ctx, t)
			ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
			f.checker.round = -1
			f.checker.rejectReprocess = true
			bad1 := *f.sign(ctx, t, 1, withdrawalExtension)
			bad1.ThresholdVoteExtensions = nil
			honest := f.sign(ctx, t, tc.honestRound, withdrawalExtension)

			sends := []commitSend{{f.bad, "attacker-0"}, {&bad1, "attacker-1"},
				{f.bad, "attacker-2"}, {&bad1, "attacker-3"}, {honest, "honest"}}
			for _, send := range sends {
				require.NoError(t, f.sendCommit(ctx, send.commit, send.peer))
			}
			if tc.processedFirst {
				crs, err := f.checker.ProcessProposal(ctx, f.block, 1, f.stateData.state, true, types.VerifiedCommit{})
				require.NoError(t, err)
				f.stateData.CurrentRoundState = crs
				f.checker.calls, f.checker.processedRounds = nil, []int32{}
			}
			require.NoError(t, f.completeBlock(ctx, false))

			f.requireCommitted(t, honest)
			require.Equal(t, tc.rounds, f.checker.processedRounds,
				"each round is processed once, and none is returned to")
		})
	}
}

// Commits rejected at two rounds leave the block processed for the later one.
// The node's own round-0 quorum then has it processed for round 0 again, which
// an application that refuses only the last round with another block, like
// kvstore, allows, and the height completes instead of panicking.
func TestOwnQuorumAfterCommitsRejectedInTwoRounds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newCommitExtFixture(ctx, t)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
	f.checker.round = -1
	f.checker.rejectReprocess = true
	// Local precommits carry no extensions; both peers' commits carry one.
	f.checker.expected = []*abci.ExtendVoteExtension{}

	require.NoError(t, f.sendCommit(ctx, f.good, "attacker-0"))
	require.NoError(t, f.sendCommit(ctx, f.sign(ctx, t, 1, withdrawalExtension), "attacker-1"))
	f.stateData.updateRoundStep(0, cstypes.RoundStepPrecommit)
	f.deliver(ctx, t, &f.stateData, f.precommit(ctx, t, f.commit.BlockID))
	require.Equal(t, cstypes.RoundStepApplyCommit, f.stateData.Step, "the quorum waits for the block")

	require.NotPanics(t, func() { require.NoError(t, f.completeBlock(ctx, false)) })

	require.Equal(t, []int32{0, 1, 0}, f.checker.processedRounds)
	require.Equal(t, f.block.Height+1, f.stateData.Height, "the own quorum must complete the height")
	require.Empty(t, f.node.blockStore.LoadSeenCommitAt(f.block.Height).ThresholdVoteExtensions)
}

// A copy of the parked commit, or of a commit its sender already queued, is
// dropped before its signature is verified. A copy of another peer's queued
// commit is kept in the sender's own slot, so the first sender cannot take it
// away by replacing its commit or disconnecting.
func TestQueuedCommitCopies(t *testing.T) {
	for _, tc := range []struct {
		name string
		// evict removes the attacker's queued authentic commit.
		evict func(ctx context.Context, t *testing.T, f *commitExtFixture, other *types.Commit)
	}{
		{name: "sender replaces its commit", evict: func(ctx context.Context, t *testing.T, f *commitExtFixture, other *types.Commit) {
			require.NoError(t, f.sendCommit(ctx, other, "attacker"))
		}},
		{name: "sender disconnects", evict: func(ctx context.Context, t *testing.T, f *commitExtFixture, other *types.Commit) {
			f.node.msgInfoQueue.purgePeer("attacker")
			require.NoError(t, f.sendCommit(ctx, other, "attacker-2"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := newCommitExtFixture(ctx, t)
			ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
			budget := &tokenBudget{tokens: 1_000}
			f.node.ctrl.Get(TryAddCommitType).(*TryAddCommitAction).verificationBudget = budget
			other := f.sign(ctx, t, 0, otherExtension)

			require.NoError(t, f.sendCommit(ctx, f.bad, "attacker"))
			require.NoError(t, f.sendCommit(ctx, f.good, "attacker"))
			charged := budget.charged
			honestCopy := *f.good
			require.NoError(t, f.sendCommit(ctx, &honestCopy, "honest"))
			require.Greater(t, budget.charged, charged, "another sender's copy is verified")
			tc.evict(ctx, t, f, other)

			require.NoError(t, f.completeBlock(ctx, false))
			f.requireCommitted(t, f.good)
		})
	}

	t.Run("sender repeats a commit", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f := newCommitExtFixture(ctx, t)
		ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
		budget := &tokenBudget{tokens: 1_000}
		f.node.ctrl.Get(TryAddCommitType).(*TryAddCommitAction).verificationBudget = budget
		candidates := f.node.ctrl.Get(TryAddCommitType).(*TryAddCommitAction).candidates
		parkedCopy, queuedCopy := *f.bad, *f.good

		require.NoError(t, f.sendCommit(ctx, f.bad, "attacker"))
		require.NoError(t, f.sendCommit(ctx, f.good, "honest"))
		charged := budget.charged
		require.NoError(t, f.sendCommit(ctx, &parkedCopy, "sybil"))
		require.NoError(t, f.sendCommit(ctx, &queuedCopy, "honest"))
		require.Equal(t, charged, budget.charged, "a copy costs no signature verification")
		require.Len(t, candidates.candidates, 1, "a copy takes no slot")

		require.NoError(t, f.completeBlock(ctx, false))
		f.requireCommitted(t, f.good)
		require.Equal(t, calls(2, "finalize"), f.checker.calls)
	})
}

// Block sync can process a block for its commit's round, have the commit's
// extensions rejected and hand over to consensus, which starts without that
// result. The authentic commit of the same round then has the application
// process the same block for that round again; an application that re-executes
// such a repeat, like Drive and kvstore, lets it apply.
func TestSameRoundReprocessedAfterHandover(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newCommitExtFixture(ctx, t)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
	f.checker.rejectReprocess = true
	_, err := f.checker.ProcessProposal(ctx, f.block, 0, f.stateData.state, true, types.VerifiedCommit{})
	require.NoError(t, err)
	require.Error(t, f.checker.VerifyVoteExtension(ctx, &types.Vote{Height: f.block.Height, Round: 0,
		BlockID: f.commit.BlockID}), "block sync's commit is rejected")
	f.checker.calls, f.checker.processedRounds = nil, nil

	require.NoError(t, f.sendCommit(ctx, f.good, "honest"))
	require.NoError(t, f.completeBlock(ctx, false))

	f.requireCommitted(t, f.good)
	require.Equal(t, []int32{0}, f.checker.processedRounds)
}

// thresholdExtension is a THRESHOLD_RECOVER extension, whose sign request ID
// no signature covers.
var thresholdExtension = tmproto.VoteExtension{Type: tmproto.VoteExtensionType_THRESHOLD_RECOVER,
	Extension: []byte("threshold")}

// signatureValidVariants returns vectors that each verify under genuine's
// threshold signatures while differing from genuine's: the list's shape, a
// THRESHOLD_RECOVER entry's sign request ID and a raw entry signed at another
// height are covered by no signature of genuine's round.
func (f *commitExtFixture) signatureValidVariants(ctx context.Context, t *testing.T, genuine *types.Commit,
	junkIDs int) []*types.Commit {
	t.Helper()
	raw, threshold := genuine.ThresholdVoteExtensions[0], genuine.ThresholdVoteExtensions[1]
	replayed := withdrawalExtension
	replayed.Extension = crypto.Checksum([]byte("earlier withdrawal"))
	replayed.XSignRequestId = &tmproto.VoteExtension_SignRequestId{SignRequestId: []byte("earlier request")}
	foreignRaw := f.signAt(ctx, t, genuine.Height+10, genuine.Round, replayed).ThresholdVoteExtensions[0]

	vectors := []tmproto.VoteExtensions{nil, {raw}, {threshold}, {threshold, raw}, {raw, threshold, raw},
		{foreignRaw, threshold}, {raw, threshold, foreignRaw}}
	for n := 2; n <= types.MaxVoteExtensions; n++ {
		dup := make(tmproto.VoteExtensions, n)
		for i := range dup {
			dup[i] = raw
		}
		vectors = append(vectors, dup)
	}
	for i := range junkIDs {
		junk := threshold.Clone()
		junk.XSignRequestId = &tmproto.VoteExtension_SignRequestId{SignRequestId: []byte(fmt.Sprintf("junk-%d", i))}
		vectors = append(vectors, tmproto.VoteExtensions{raw, &junk})
	}

	sd := &f.stateData
	variants := make([]*types.Commit, 0, len(vectors))
	for _, vector := range vectors {
		variant := *genuine
		variant.ThresholdVoteExtensions = vector
		require.NoError(t, sd.Validators.VerifyCommit(sd.state.ChainID, variant.BlockID, variant.Height, &variant))
		variants = append(variants, &variant)
	}
	return variants
}

// Only the block and round of a commit are authenticated; its extension list
// is not, so a peer holding genuine commits of two rounds has any number of
// signature-valid vectors to alternate between. Once the application's
// expectation for a round is learned, a commit that would have the block
// processed again for that round is refused without it unless it carries the
// expected vector, so the block is processed a bounded number of times however
// many vectors arrive, and the genuine commit still finishes the height.
func TestForeignRoundCommitsCannotForceReprocessing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newCommitExtFixture(ctx, t)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
	f.checker.round = -1
	genuine := map[int32]*types.Commit{}
	variants := map[int32][]*types.Commit{}
	for _, round := range []int32{1, 2} {
		genuine[round] = f.sign(ctx, t, round, withdrawalExtension, thresholdExtension)
		variants[round] = f.signatureValidVariants(ctx, t, genuine[round], 20)
	}
	expected, err := genuine[1].GetCanonicalVote()
	require.NoError(t, err)
	f.checker.expected = expected.VoteExtensions.ToExtendProto()
	require.Greater(t, len(variants[1])+len(variants[2]), 66, "more vectors than a 64-entry verdict cache holds")
	// The attack is bounded here by the expectations, not by the rate-limited
	// verification budget, which this many commits would outrun on a slow run.
	f.node.ctrl.Get(TryAddCommitType).(*TryAddCommitAction).verificationBudget = &tokenBudget{tokens: 1_000_000}
	f.holdBlock(ctx, t)
	f.checker.processedRounds = nil
	counter := &recordingCounter{}
	f.node.metrics.CommitVerifyFailures = counter

	verifies := 0
	for i := range variants[1] {
		for _, round := range []int32{1, 2} {
			if f.checker.processedLast(round, f.block.Hash()) {
				verifies++ // the round processed last is the application's to judge
			}
			require.ErrorIs(t, f.sendCommit(ctx, variants[round][i], "attacker"), sm.ErrCommitExtensionsRejected)
		}
	}

	const foreignRounds = 2
	require.Equal(t, []int32{1, 2}, f.checker.processedRounds,
		"each round is processed once, to learn what the application expects")
	require.LessOrEqual(t, len(f.checker.processedRounds), 2*foreignRounds)
	require.Equal(t, []int32{1, 2}, f.checker.extendedRounds, "the expectation is learned once per round")
	require.Len(t, slices.DeleteFunc(slices.Clone(f.checker.calls), func(c string) bool { return c != "verify" }),
		foreignRounds+verifies, "a commit refused before processing never reaches the application")
	require.Equal(t, float64(2*len(variants[1])), counter.value, "every refusal is counted")
	require.Zero(t, f.stateData.Round, "a rejected commit must not change the round")
	select {
	case report := <-f.node.peerErrorQueue.ch:
		t.Fatalf("a refused commit must not evict its sender: %v", report)
	default:
	}

	f.checker.calls = nil
	require.NoError(t, f.sendCommit(ctx, genuine[1], "honest"))
	f.requireCommitted(t, genuine[1])
	require.Equal(t, []int32{1, 2, 1}, f.checker.processedRounds, "the genuine commit has its round processed again")
	require.Equal(t, []string{"process", "verify", "finalize"}, f.checker.calls[:3])
}

// An application that rejects the very vector its ExtendVote returned breaks
// the contract, and its rejection is remembered: the vector is refused
// without processing the block for that round again.
func TestRejectedExpectedVectorIsRemembered(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newCommitExtFixture(ctx, t)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
	f.checker.round = -1
	f.checker.expected = []*abci.ExtendVoteExtension{}
	extended, err := f.good.GetCanonicalVote()
	require.NoError(t, err)
	f.checker.extended = extended.VoteExtensions.ToExtendProto()
	one, two := f.sign(ctx, t, 1, withdrawalExtension), f.sign(ctx, t, 2, withdrawalExtension)
	f.holdBlock(ctx, t)
	f.checker.processedRounds = nil

	for range 3 {
		require.ErrorIs(t, f.sendCommit(ctx, one, "attacker"), sm.ErrCommitExtensionsRejected)
		require.ErrorIs(t, f.sendCommit(ctx, two, "attacker"), sm.ErrCommitExtensionsRejected)
	}
	require.Equal(t, []int32{1, 2}, f.checker.processedRounds)
	require.Equal(t, []int32{1, 2}, f.checker.extendedRounds)
	require.Equal(t, []string{"process", "verify", "process", "verify"}, f.checker.calls,
		"neither round's expected vector is judged twice")
}

// A rejected commit of another round leaves the application processing that
// round. Before the node asks it about its own round again, verifying a peer's
// precommit or extending its own on a relock, it has the round's block
// processed for that round again: Drive answers from the round processed last
// and fails ExtendVote for any other.
func TestOwnRoundRestoredAfterForeignCommitRejected(t *testing.T) {
	for _, tc := range []string{"peer precommit", "relock"} {
		t.Run(tc, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := newCommitExtFixture(ctx, t)
			ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
			f.checker.round = -1
			f.checker.checkPrecommitContext = true
			f.node.voteSigner.voteExtender = f.checker
			sd := &f.stateData
			sd.Proposal = &types.Proposal{Height: f.block.Height, Round: 0, POLRound: -1,
				BlockID: f.commit.BlockID, Timestamp: f.block.Time}
			f.holdBlock(ctx, t)
			future := *f.sign(ctx, t, 2, withdrawalExtension)
			future.ThresholdVoteExtensions = nil
			require.ErrorIs(t, f.sendCommit(ctx, &future, "attacker"), sm.ErrCommitExtensionsRejected)
			require.Zero(t, sd.Round)
			require.True(t, f.checker.processedLast(2, f.block.Hash()), "the rejected commit's round is processed last")
			f.checker.processedRounds, f.checker.extendedRounds = nil, nil

			switch tc {
			case "peer precommit":
				var peerVote *types.Vote
				for _, vote := range f.precommit(ctx, t, f.commit.BlockID) {
					if !bytes.Equal(vote.ValidatorProTxHash, f.node.privValidator.ProTxHash) {
						peerVote = vote
					}
				}
				require.NotNil(t, peerVote)
				f.deliver(ctx, t, sd, []*types.Vote{peerVote})
				require.NotNil(t, sd.Votes.Precommits(0).GetByIndex(peerVote.ValidatorIndex),
					"the precommit is verified against its own round")
			case "relock":
				sd.LockedRound, sd.LockedBlock, sd.LockedBlockParts = 0, f.block, f.parts
				sd.updateRoundStep(0, cstypes.RoundStepPrevote)
				require.NotPanics(t, func() { f.deliver(ctx, t, sd, f.prevote(ctx, t, f.commit.BlockID)) })
				require.Equal(t, cstypes.RoundStepPrecommit, sd.Step)
				require.Equal(t, []int32{0}, f.checker.extendedRounds, "the relock precommit is extended")
			}
			require.Equal(t, []int32{0}, f.checker.processedRounds, "the round's block is processed for it again")
		})
	}
}

// A held block's commit for a later round is checked before the node enters that
// round. If the node proposes that round, it must apply the checked commit
// rather than build a competing proposal: PrepareProposal would replace the
// processed result, and an application that refuses the same round with
// another block, like Drive, would then fail the ProcessProposal that applying
// needs.
func TestHeldFutureCommitSkipsOwnProposal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newCommitExtFixture(ctx, t)
	require.False(t, f.node.config.DontAutoPropose)
	ctx = dash.ContextWithProTxHash(ctx, f.node.privValidator.ProTxHash)
	round := int32(1)
	for {
		proposer, err := f.stateData.ProposerSelector.GetProposer(f.stateData.Height, round)
		require.NoError(t, err)
		if bytes.Equal(proposer.ProTxHash, f.node.privValidator.ProTxHash) {
			break
		}
		round++
	}
	f.checker.round = round
	f.checker.rejectReprocess = true
	f.holdBlock(ctx, t)
	f.checker.processedRounds = nil
	future := f.sign(ctx, t, round, withdrawalExtension)

	require.NotPanics(t, func() { require.NoError(t, f.sendCommit(ctx, future, "honest")) })

	f.requireCommitted(t, future)
	require.Equal(t, []int32{round}, f.checker.processedRounds, "the round is processed once")
	// The next height may be proposed once this one is finalized.
	require.Equal(t, []string{"process", "verify", "finalize"}, f.checker.calls[:3],
		"no proposal is prepared for the committed height")
}

// commitCheckingExecutor plays an application that accepts only the expected
// commit extension vector. Individual precommits go to the wrapped executor.
type commitCheckingExecutor struct {
	sm.Executor
	t     *testing.T
	block *types.Block
	// round is the round every commit must be for; -1 accepts any.
	round    int32
	expected []*abci.ExtendVoteExtension
	store    sm.BlockStore
	calls    []string
	// onVerify and onFinalize observe the node while it applies a commit.
	onVerify   func(vote *types.Vote)
	onFinalize func(commit *types.Commit)
	// processed remembers each round's result, which a later ProcessProposal
	// for that round returns again.
	processed map[int32]sm.CurrentRoundState
	// rejectReprocess fails a ProcessProposal for the round processed last with
	// a different block, as Drive and kvstore do; the same block again, or any
	// other round, is processed again.
	rejectReprocess bool
	// lastRound and lastHash identify the latest ProcessProposal; lastRound is
	// nil before any.
	lastRound *int32
	lastHash  tmbytes.HexBytes
	// processedRounds lists the round of every ProcessProposal call.
	processedRounds []int32
	// extendedRounds lists the round of every ExtendVote call. ExtendVote
	// returns extended if set, or else expected; like Drive's, it fails for a
	// block and round other than those processed last.
	extendedRounds []int32
	extended       []*abci.ExtendVoteExtension
	// checkPrecommitContext rejects a validator's precommit unless its block and
	// round are the ones processed last.
	checkPrecommitContext bool
}

// processedLast reports whether the latest ProcessProposal was for round and
// the block hash.
func (e *commitCheckingExecutor) processedLast(round int32, hash []byte) bool {
	return e.lastRound != nil && *e.lastRound == round && bytes.Equal(e.lastHash, hash)
}

func (e *commitCheckingExecutor) ExtendVote(_ context.Context, vote *types.Vote) {
	if !e.processedLast(vote.Round, vote.BlockID.Hash) {
		panic(fmt.Sprintf("ExtendVote for round %d does not match the block and round processed last", vote.Round))
	}
	e.extendedRounds = append(e.extendedRounds, vote.Round)
	response := e.expected
	if e.extended != nil {
		response = e.extended
	}
	extensions, err := types.NewVoteExtensionsFromABCIExtended(response)
	require.NoError(e.t, err)
	vote.VoteExtensions = extensions
}

func (e *commitCheckingExecutor) ProcessProposal(ctx context.Context, block *types.Block, round int32,
	state sm.State, verify bool, last types.VerifiedCommit) (sm.CurrentRoundState, error) {
	e.calls = append(e.calls, "process")
	e.processedRounds = append(e.processedRounds, round)
	if e.rejectReprocess && e.lastRound != nil && *e.lastRound == round && !bytes.Equal(e.lastHash, block.Hash()) {
		return sm.CurrentRoundState{}, fmt.Errorf("duplicate ProcessProposal call at height %d, round %d",
			block.Height, round)
	}
	e.lastRound, e.lastHash = &round, block.Hash()
	if crs, ok := e.processed[round]; ok {
		return crs, nil
	}
	crs, err := e.Executor.ProcessProposal(ctx, block, round, state, verify, last)
	if err == nil {
		if e.processed == nil {
			e.processed = map[int32]sm.CurrentRoundState{}
		}
		e.processed[round] = crs
	}
	return crs, err
}

func (e *commitCheckingExecutor) CreateProposalBlock(ctx context.Context, height int64, round int32, state sm.State,
	commit *types.Commit, proposer []byte, appVersion uint64) (*types.Block, sm.CurrentRoundState, error) {
	e.calls = append(e.calls, "prepare")
	return e.Executor.CreateProposalBlock(ctx, height, round, state, commit, proposer, appVersion)
}

func (e *commitCheckingExecutor) VerifyVoteExtension(ctx context.Context, vote *types.Vote) error {
	if len(vote.ValidatorProTxHash) != 0 {
		if e.checkPrecommitContext && !e.processedLast(vote.Round, vote.BlockID.Hash) {
			return fmt.Errorf("precommit for round %d does not match the block and round processed last", vote.Round)
		}
		return e.Executor.VerifyVoteExtension(ctx, vote)
	}
	require.Zero(e.t, e.store.Height(), "verification must precede persistence")
	require.True(e.t, e.processedLast(vote.Round, vote.BlockID.Hash),
		"a commit is verified only in the context of its own round's ProcessProposal")
	require.Equal(e.t, e.block.Hash(), vote.BlockID.Hash)
	require.Equal(e.t, e.block.Height, vote.Height)
	if e.round >= 0 {
		require.Equal(e.t, e.round, vote.Round)
	}
	e.calls = append(e.calls, "verify")
	if e.onVerify != nil {
		e.onVerify(vote)
	}
	if !reflect.DeepEqual(e.expected, vote.VoteExtensions.ToExtendProto()) {
		return errors.New("invalid vote extension")
	}
	return nil
}

func (e *commitCheckingExecutor) FinalizeBlock(ctx context.Context, state sm.State, rs sm.CurrentRoundState,
	id types.BlockID, block *types.Block, commit *types.Commit, last types.VerifiedCommit) (sm.State, *abci.ResponseFinalizeBlock, error) {
	require.Equal(e.t, block.Height, e.store.Height(), "save must still precede finalization")
	require.Equal(e.t, commit.Round, rs.Round, "FinalizeBlock must get the result processed for the commit's round")
	require.True(e.t, e.processedLast(commit.Round, block.Hash()),
		"the application must finalize the round it processed last")
	e.calls = append(e.calls, "finalize")
	if e.onFinalize != nil {
		e.onFinalize(commit)
	}
	return e.Executor.FinalizeBlock(ctx, state, rs, id, block, commit, last)
}
