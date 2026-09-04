package consensus

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/types"
)

// TestHandleCommitVerifyErrorClassification pins which commit-verification
// failures evict a peer. Only a failed threshold-signature check is unambiguous
// misbehavior; every other failure (wrong block ID, wrong quorum hash, a local
// fault) is reachable by an honest relayer or a forked peer and must never cause
// a disconnect. Replayed messages carry the original PeerID, so they are exempt
// regardless of the error.
func TestHandleCommitVerifyErrorClassification(t *testing.T) {
	const peerID = types.NodeID("peer-under-test")

	testCases := []struct {
		name       string
		err        error
		fromReplay bool
		wantEvict  bool
	}{
		{
			name:      "invalid threshold signature evicts",
			err:       types.ErrInvalidCommitSignature{Err: errors.New("threshold signature did not verify")},
			wantEvict: true,
		},
		{
			name:      "wrapped invalid threshold signature evicts",
			err:       fmt.Errorf("error verifying commit: %w", types.ErrInvalidCommitSignature{}),
			wantEvict: true,
		},
		{
			name: "wrong block ID does not evict",
			err: fmt.Errorf("error verifying commit: %w",
				fmt.Errorf("invalid commit -- wrong block ID: want %v, got %v", types.BlockID{}, types.BlockID{})),
			wantEvict: false,
		},
		{
			name:      "wrong quorum hash does not evict",
			err:       types.ErrInvalidCommitQuorumHash{Expected: []byte{0x1}, Actual: []byte{0x2}},
			wantEvict: false,
		},
		{
			name:      "local finalization fault does not evict",
			err:       fmt.Errorf("+2/3 committed an invalid block: %w", errors.New("app hash mismatch")),
			wantEvict: false,
		},
		{
			name:      "verification budget exhaustion does not evict",
			err:       fmt.Errorf("error verifying commit: %w", types.ErrVerificationBudgetExhausted),
			wantEvict: false,
		},
		{
			name:       "invalid threshold signature from replay does not evict",
			err:        types.ErrInvalidCommitSignature{Err: errors.New("threshold signature did not verify")},
			fromReplay: true,
			wantEvict:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			queue := &chanQueue[peerErrorMsg]{ch: make(chan peerErrorMsg, 4)}
			action := &TryAddCommitAction{
				peerErrorQueue: queue,
				metrics:        NopMetrics(),
			}

			action.handleCommitVerifyError(tc.err, peerID, tc.fromReplay)

			if !tc.wantEvict {
				require.Empty(t, queue.ch, "peer must not be evicted for %v", tc.err)
				return
			}

			require.Len(t, queue.ch, 1, "expected an eviction report")
			got := <-queue.ch
			assert.Equal(t, peerID, got.PeerID)
			assert.True(t, got.Fatal, "eviction report must be fatal to disconnect the peer")
			assert.ErrorAs(t, got.Err, &types.ErrInvalidCommitSignature{})
		})
	}
}

func TestHandleCommitVerifyErrorRecordsPeerVerificationBudgetDrop(t *testing.T) {
	testCases := []struct {
		name            string
		peerID          types.NodeID
		fromReplay      bool
		verificationErr error
		wantDrops       float64
	}{
		{
			name:            "remote commit",
			peerID:          "peer",
			verificationErr: fmt.Errorf("error verifying commit: %w", types.ErrVerificationBudgetExhausted),
			wantDrops:       1,
		},
		{
			name:            "remote non-budget error",
			peerID:          "peer",
			verificationErr: errors.New("signature verification failed"),
		},
		{
			name:            "local commit",
			verificationErr: types.ErrVerificationBudgetExhausted,
		},
		{
			name:            "replayed commit",
			peerID:          "peer",
			fromReplay:      true,
			verificationErr: types.ErrVerificationBudgetExhausted,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			counter := &recordingCounter{}
			metrics := NopMetrics()
			metrics.VerificationBudgetDrops = counter
			action := &TryAddCommitAction{metrics: metrics}

			action.handleCommitVerifyError(tc.verificationErr, tc.peerID, tc.fromReplay)

			require.Equal(t, tc.wantDrops, counter.value)
		})
	}
}

// TestHandleCommitVerifyErrorNilQueue ensures a nil queue (as used by tests that
// build the action directly) is tolerated rather than panicking.
func TestHandleCommitVerifyErrorNilQueue(t *testing.T) {
	action := &TryAddCommitAction{}
	assert.NotPanics(t, func() {
		action.handleCommitVerifyError(types.ErrInvalidCommitSignature{}, "peer", false)
	})
}

// TestHandleCommitVerifyErrorQueueFull ensures a saturated queue drops the
// report instead of blocking the consensus goroutine.
func TestHandleCommitVerifyErrorQueueFull(t *testing.T) {
	queue := &chanQueue[peerErrorMsg]{ch: make(chan peerErrorMsg, 1)}
	queue.ch <- peerErrorMsg{PeerID: "other"}

	action := &TryAddCommitAction{peerErrorQueue: queue}
	assert.NotPanics(t, func() {
		action.handleCommitVerifyError(types.ErrInvalidCommitSignature{}, "peer", false)
	})
	assert.Len(t, queue.ch, 1, "the pre-existing report must be preserved")
}

// TestTryAddCommitWithAssembledBlockAndStaleProposal covers a commit that arrives
// after the block it commits has been fully assembled, while a Proposal for a
// block the network dropped is still around: the +2/3 prevote majority that
// retargeted ProposalBlockParts left the Proposal untouched
// (addVoteUpdateValidBlockMw). Deciding from that Proposal rejects the commit,
// and because the part set is already complete no later part can retry it while
// the parked StateData.Commit turns every further commit into a no-op — the node
// stalls at this height holding the very block it needs
// (dashpay/tenderdash#1414).
func TestTryAddCommitWithAssembledBlockAndStaleProposal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := configSetup(t)

	n := newStaleProposalNode(ctx, t, cfg, types.BlockPartSizeBytes, 0)
	stateData := n.node.GetStateData()

	staleProposal := types.NewProposal(
		n.block.Height, n.block.CoreChainLockedHeight, 0, -1, factory.MakeBlockID(), n.block.Time)

	stateData.Proposal = staleProposal
	stateData.ProposalBlock = n.block
	stateData.ProposalBlockParts = n.parts
	stateData.updateRoundStep(n.commit.Round, cstypes.RoundStepPrevote)

	ctx = dash.ContextWithProTxHash(ctx, n.node.privValidator.ProTxHash)
	ctx = msgInfoWithCtx(ctx, msgInfo{Msg: &CommitMessage{n.commit}, PeerID: n.peerID})

	require.NoError(t, n.node.ctrl.Dispatch(ctx, &TryAddCommitEvent{Commit: n.commit, PeerID: n.peerID}, &stateData))
	assert.Equal(t, int64(2), stateData.Height,
		"a commit for the block we hold must be applied rather than dropped over a proposal that outlived its own block")
}

// TestTryAddCommitForFutureRoundParksCommitAndPartSet drives a commit for a round
// ahead of ours through the real Controller. adoptCommit retargets the part set,
// EnterNewRound then wipes the whole proposal state for a round > 0, and
// TryAddCommitAction rebuilds the part set from the same header afterwards. The
// end state is correct only because of that ordering, and a one-sided change to
// either half would go unnoticed without this test.
func TestTryAddCommitForFutureRoundParksCommitAndPartSet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := configSetup(t)

	const futureRound = int32(1)
	n := newStaleProposalNode(ctx, t, cfg, types.BlockPartSizeBytes, futureRound)
	stateData := n.node.GetStateData()

	stateData.Proposal = types.NewProposal(
		n.block.Height, n.block.CoreChainLockedHeight, 0, -1, factory.MakeBlockID(), n.block.Time)
	stateData.updateRoundStep(0, cstypes.RoundStepPrevote)
	require.Less(t, stateData.Round, n.commit.Round, "the commit must name a round ahead of ours")

	ctx = dash.ContextWithProTxHash(ctx, n.node.privValidator.ProTxHash)
	ctx = msgInfoWithCtx(ctx, msgInfo{Msg: &CommitMessage{n.commit}, PeerID: n.peerID})

	require.NoError(t, n.node.ctrl.Dispatch(ctx, &TryAddCommitEvent{Commit: n.commit, PeerID: n.peerID}, &stateData))

	assert.Equal(t, futureRound, stateData.Round, "a verified commit for a later round must move us to that round")
	assert.Same(t, n.commit, stateData.Commit, "the commit must be kept until the block it commits arrives")
	require.NotNil(t, stateData.ProposalBlockParts, "the node must be ready to receive the committed block")
	assert.True(t, stateData.ProposalBlockParts.HasHeader(n.commit.BlockID.PartSetHeader),
		"the part set must target the committed block")
}
