package consensus

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/abci/example/kvstore"
	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sf "github.com/dashpay/tenderdash/internal/state/test/factory"
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
			err:       fmt.Errorf("invalid commit -- wrong quorum hash: validator set uses %X, commit has %X", []byte{0x1}, []byte{0x2}),
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

	css := makeConsensusState(ctx, t, cfg, 2, t.Name(), newTickerFunc())
	privVals := make([]types.PrivValidator, 0, len(css))
	for _, c := range css {
		privVals = append(privVals, c.privValidator.PrivValidator)
	}
	proposer, otherNode := css[0], css[1]
	proposerStateData := proposer.GetStateData()
	stateData := otherNode.GetStateData()

	block, err := sf.MakeBlock(proposerStateData.state, 1, &types.Commit{}, kvstore.ProtocolVersion)
	require.NoError(t, err)
	block.CoreChainLockedHeight = 1
	parts, err := block.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)

	commit, err := factory.MakeCommit(
		ctx,
		block.BlockID(parts),
		block.Height,
		0,
		proposerStateData.Votes.Precommits(0),
		proposerStateData.Validators,
		privVals,
	)
	require.NoError(t, err)

	staleProposal := types.NewProposal(block.Height, block.CoreChainLockedHeight, 0, -1, factory.MakeBlockID(), block.Time)

	peerID := proposerStateData.Validators.Proposer().NodeAddress.NodeID
	stateData.Proposal = staleProposal
	stateData.ProposalBlock = block
	stateData.ProposalBlockParts = parts
	stateData.updateRoundStep(commit.Round, cstypes.RoundStepPrevote)

	ctx = dash.ContextWithProTxHash(ctx, otherNode.privValidator.ProTxHash)
	ctx = msgInfoWithCtx(ctx, msgInfo{Msg: &CommitMessage{commit}, PeerID: peerID})

	require.NoError(t, otherNode.ctrl.Dispatch(ctx, &TryAddCommitEvent{Commit: commit, PeerID: peerID}, &stateData))
	assert.Equal(t, int64(2), stateData.Height,
		"a commit for the block we hold must be applied rather than dropped over a proposal that outlived its own block")
}
