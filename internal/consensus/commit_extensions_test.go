package consensus

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func TestCommitExtensionsRejectedBeforeSaveAndRetried(t *testing.T) {
	for _, path := range []string{"held", "parked", "future", "local", "replay"} {
		for _, mutation := range []string{"strip", "duplicate", "cross-height replay"} {
			t.Run(path+"/"+mutation, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				cfg := configSetup(t)
				cfg.Consensus.DontAutoPropose = true
				round := int32(0)
				if path == "future" {
					round = 2
				}
				n := newCommitFixture(ctx, t, cfg, types.BlockPartSizeBytes, round)
				sd := n.node.GetStateData()
				ext := tmproto.VoteExtension{Type: tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW,
					Extension:      crypto.Checksum([]byte("withdrawal")),
					XSignRequestId: &tmproto.VoteExtension_SignRequestId{SignRequestId: []byte("withdrawal-request")}}
				sign := func(height int64, extension tmproto.VoteExtension) *types.Commit {
					votes := types.NewVoteSet(sd.state.ChainID, height, round, tmproto.PrecommitType, sd.Validators)
					commit, err := factory.MakeCommit(ctx, n.commit.BlockID, height, round, votes, sd.Validators, n.privVals, extension)
					require.NoError(t, err)
					return commit
				}
				good := sign(n.block.Height, ext)
				badValue := *good
				bad := &badValue
				switch mutation {
				case "strip":
					bad.ThresholdVoteExtensions = nil
				case "duplicate":
					bad.ThresholdVoteExtensions = append(bad.ThresholdVoteExtensions, bad.ThresholdVoteExtensions[0])
				case "cross-height replay":
					old := ext
					old.Extension = crypto.Checksum([]byte("previous withdrawal"))
					old.XSignRequestId = &tmproto.VoteExtension_SignRequestId{SignRequestId: []byte("previous request")}
					bad.ThresholdVoteExtensions = sign(n.block.Height+10, old).ThresholdVoteExtensions
				}
				require.NoError(t, sd.Validators.VerifyCommit(sd.state.ChainID, bad.BlockID, bad.Height, bad))
				expected, err := good.GetCanonicalVote()
				require.NoError(t, err)
				checker := &commitCheckingExecutor{Executor: n.node.blockExecutor.blockExec, t: t, block: n.block,
					round: round, expected: expected.VoteExtensions.ToExtendProto(), store: n.node.blockStore}
				n.node.blockExecutor.blockExec = checker
				sd.updateRoundStep(0, cstypes.RoundStepPrevote)
				ctx = dash.ContextWithProTxHash(ctx, n.node.privValidator.ProTxHash)
				ctx = msgInfoWithCtx(ctx, msgInfo{Msg: &CommitMessage{bad}, PeerID: n.peerID})
				parked := path == "parked" || path == "future" || path == "replay"
				checker.onVerify = func(*types.Vote) {
					if !parked {
						require.Nil(t, sd.Commit, "an unverified commit must not be published in the round state")
					}
				}
				checker.onFinalize = func(commit *types.Commit) {
					require.Same(t, commit, sd.Commit, "the accepted commit must be published in the round state")
				}
				if !parked {
					sd.ProposalBlock, sd.ProposalBlockParts = n.block, n.parts
				}
				if path == "local" {
					sd.updateRoundStep(round, cstypes.RoundStepApplyCommit)
					sd.CommitRound = round
					err = n.node.ctrl.Dispatch(ctx, &ApplyCommitEvent{Commit: bad}, &sd)
				} else {
					err = n.node.ctrl.Dispatch(ctx, &TryAddCommitEvent{Commit: bad, PeerID: n.peerID, FromReplay: path == "replay"}, &sd)
					if parked {
						require.NoError(t, err)
						require.Empty(t, checker.calls, "cannot check until the block is processed")
						msg := &BlockPartMessage{Height: n.block.Height, Round: round, Part: n.parts.GetPart(0)}
						partCtx := msgInfoWithCtx(ctx, msgInfo{Msg: msg, PeerID: n.peerID})
						err = n.node.ctrl.Dispatch(partCtx, &AddProposalBlockPartEvent{Msg: msg, PeerID: n.peerID, FromReplay: path == "replay"}, &sd)
					}
				}
				require.Error(t, err, "a cryptographically valid but wrong extension list must be rejected")
				require.Equal(t, []string{"process", "verify"}, checker.calls)
				require.Zero(t, n.node.blockStore.Height())
				require.Equal(t, n.block.Height, sd.Height)
				require.Nil(t, sd.Commit, "a rejected parked commit must not block a replacement")
				// The persisted copy, not sd: a restart must not resume the rejected commit.
				sd = n.node.GetStateData()
				require.Nil(t, sd.Commit)
				require.Less(t, sd.Step, cstypes.RoundStepApplyCommit)
				require.Equal(t, int32(-1), sd.CommitRound)
				require.True(t, sd.CommitTime.IsZero())
				require.NoError(t, n.node.ctrl.Dispatch(ctx, &TryAddCommitEvent{Commit: good, PeerID: n.peerID}, &sd))
				require.Equal(t, n.block.Height+1, sd.Height)
				require.Equal(t, n.block.Height, n.node.blockStore.Height())
				require.Equal(t, []string{"process", "verify", "verify", "finalize"}, checker.calls)
			})
		}
	}
}

// commitCheckingExecutor plays an application that accepts only the expected
// commit extension vector. Individual precommits go to the wrapped executor.
type commitCheckingExecutor struct {
	sm.Executor
	t        *testing.T
	block    *types.Block
	round    int32
	expected []*abci.ExtendVoteExtension
	store    sm.BlockStore
	calls    []string
	// onVerify and onFinalize observe the node while it applies a commit.
	onVerify   func(vote *types.Vote)
	onFinalize func(commit *types.Commit)
	// rejectAll rejects every commit vector, however correct.
	rejectAll bool
	// processed remembers each round's result: like Drive, and unlike the
	// kvstore app, the application processes a block again after proposing in a
	// later round.
	processed map[int32]sm.CurrentRoundState
}

func (e *commitCheckingExecutor) ProcessProposal(ctx context.Context, block *types.Block, round int32,
	state sm.State, verify bool, last types.VerifiedCommit) (sm.CurrentRoundState, error) {
	e.calls = append(e.calls, "process")
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

func (e *commitCheckingExecutor) VerifyVoteExtension(ctx context.Context, vote *types.Vote) error {
	if len(vote.ValidatorProTxHash) != 0 {
		return e.Executor.VerifyVoteExtension(ctx, vote)
	}
	require.Zero(e.t, e.store.Height(), "verification must precede persistence")
	require.Equal(e.t, e.block.Hash(), vote.BlockID.Hash)
	require.Equal(e.t, e.block.Height, vote.Height)
	require.Equal(e.t, e.round, vote.Round)
	e.calls = append(e.calls, "verify")
	if e.onVerify != nil {
		e.onVerify(vote)
	}
	if e.rejectAll || !reflect.DeepEqual(e.expected, vote.VoteExtensions.ToExtendProto()) {
		return errors.New("invalid vote extension")
	}
	return nil
}

func (e *commitCheckingExecutor) FinalizeBlock(ctx context.Context, state sm.State, rs sm.CurrentRoundState,
	id types.BlockID, block *types.Block, commit *types.Commit, last types.VerifiedCommit) (sm.State, *abci.ResponseFinalizeBlock, error) {
	require.Equal(e.t, block.Height, e.store.Height(), "save must still precede finalization")
	e.calls = append(e.calls, "finalize")
	if e.onFinalize != nil {
		e.onFinalize(commit)
	}
	return e.Executor.FinalizeBlock(ctx, state, rs, id, block, commit, last)
}

func TestCommitEmptyExtensionsStillVerified(t *testing.T) {
	for _, round := range []int32{0, 2} {
		t.Run(fmt.Sprint(round), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cfg := configSetup(t)
			cfg.Consensus.DontAutoPropose = true
			n := newCommitFixture(ctx, t, cfg, types.BlockPartSizeBytes, round)
			sd := n.node.GetStateData()
			checker := &commitCheckingExecutor{Executor: n.node.blockExecutor.blockExec, t: t, block: n.block,
				round: round, expected: []*abci.ExtendVoteExtension{}, store: n.node.blockStore}
			n.node.blockExecutor.blockExec = checker
			sd.ProposalBlock, sd.ProposalBlockParts = n.block, n.parts
			sd.updateRoundStep(0, cstypes.RoundStepPrevote)
			ctx = dash.ContextWithProTxHash(ctx, n.node.privValidator.ProTxHash)
			ctx = msgInfoWithCtx(ctx, msgInfo{Msg: &CommitMessage{n.commit}, PeerID: n.peerID})
			require.NoError(t, n.node.ctrl.Dispatch(ctx, &TryAddCommitEvent{Commit: n.commit, PeerID: n.peerID}, &sd))
			require.Equal(t, []string{"process", "verify", "finalize"}, checker.calls)
		})
	}
}
