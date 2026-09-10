package consensus

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

func TestPendingCommitPreservesDownloadAcrossRounds(t *testing.T) {
	for _, commitRound := range []int32{0, 2} {
		t.Run(fmt.Sprintf("commit_round_%d", commitRound), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cfg := configSetup(t)
			cfg.Consensus.DontAutoPropose = true
			n := newCommitFixture(ctx, t, cfg, multiPartBlockPartSize, commitRound)
			stateData := n.node.GetStateData()
			stateData.Proposal = types.NewProposal(n.block.Height, n.block.CoreChainLockedHeight,
				0, -1, n.commit.BlockID, n.block.Time)
			partial := types.NewPartSetFromHeader(n.commit.BlockID.PartSetHeader)
			added, err := partial.AddPart(n.parts.GetPart(0))
			require.NoError(t, err)
			require.True(t, added)
			stateData.ProposalBlockParts = partial
			stateData.updateRoundStep(0, cstypes.RoundStepPrecommitWait)
			ctx = dash.ContextWithProTxHash(ctx, n.node.privValidator.ProTxHash)
			commitCtx := msgInfoWithCtx(ctx, msgInfo{Msg: &CommitMessage{n.commit}, PeerID: n.peerID})
			require.NoError(t, n.node.ctrl.Dispatch(commitCtx,
				&TryAddCommitEvent{Commit: n.commit, PeerID: n.peerID}, &stateData))

			// Receiving a commit does not cancel timeouts already queued for the round.
			stateData.updateRoundStep(commitRound, cstypes.RoundStepPrecommitWait)
			n.node.handleTimeout(ctx, timeoutInfo{
				Height: stateData.Height, Round: commitRound, Step: cstypes.RoundStepPrecommitWait,
			}, &stateData)
			require.Equal(t, commitRound+1, stateData.Round)
			require.Same(t, partial, stateData.ProposalBlockParts)
			assert.Equal(t, uint32(1), stateData.ProposalBlockParts.Count())
			assert.Same(t, n.commit, stateData.Commit)
			assert.Nil(t, stateData.Proposal)

			for i := 1; i < int(n.parts.Total()); i++ {
				msg := &BlockPartMessage{Height: n.block.Height, Round: commitRound, Part: n.parts.GetPart(i)}
				partCtx := msgInfoWithCtx(ctx, msgInfo{Msg: msg, PeerID: n.peerID})
				require.NoError(t, n.node.ctrl.Dispatch(partCtx,
					&AddProposalBlockPartEvent{Msg: msg, PeerID: n.peerID}, &stateData))
			}
			assert.Equal(t, n.block.Height+1, stateData.Height)
		})
	}
}

func TestPendingCommitAnnouncesDownloadTarget(t *testing.T) {
	for _, commitRound := range []int32{0, 2} {
		t.Run(fmt.Sprintf("commit_round_%d", commitRound), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cfg := configSetup(t)
			cfg.Consensus.DontAutoPropose = true
			n := newCommitFixture(ctx, t, cfg, multiPartBlockPartSize, commitRound)
			stateData := n.node.GetStateData()
			staleID := factory.MakeBlockID()
			stateData.Proposal = types.NewProposal(n.block.Height, n.block.CoreChainLockedHeight,
				0, -1, staleID, n.block.Time)
			stateData.ProposalBlockParts = types.NewPartSetFromHeader(staleID.PartSetHeader)
			stateData.updateRoundStep(0, cstypes.RoundStepPrevote)

			// A sender may already have observed a later round from this receiver.
			peer := NewPeerState(log.NewNopLogger(), "receiver")
			peer.PRS.Height = stateData.Height
			peer.PRS.Round = commitRound + 1
			peer.PRS.ProposalBlockPartSetHeader = staleID.PartSetHeader
			peer.PRS.ProposalBlockParts = stateData.ProposalBlockParts.BitArray()
			announcements := 0
			n.node.emitter.AddListener(types.EventValidBlockValue, func(data eventemitter.EventData) error {
				rs := data.(*cstypes.RoundState)
				msg, err := MsgFromProto(rs.NewValidBlockMessage())
				require.NoError(t, err)
				validBlock := msg.(*NewValidBlockMessage)
				require.True(t, validBlock.IsCommit)
				peer.ApplyNewValidBlockMessage(validBlock)
				announcements++
				return nil
			})
			ctx = dash.ContextWithProTxHash(ctx, n.node.privValidator.ProTxHash)
			ctx = msgInfoWithCtx(ctx, msgInfo{Msg: &CommitMessage{n.commit}, PeerID: n.peerID})
			require.NoError(t, n.node.ctrl.Dispatch(ctx,
				&TryAddCommitEvent{Commit: n.commit, PeerID: n.peerID}, &stateData))
			require.Equal(t, 1, announcements)

			prs := peer.GetRoundState()
			assert.True(t, prs.ProposalBlockPartSetHeader.Equals(n.commit.BlockID.PartSetHeader))
			sender := cstypes.RoundState{
				Height: n.block.Height, Round: prs.Round, ProposalBlockParts: n.parts,
			}
			assert.True(t, shouldBlockPartsBeGossiped(sender, prs, true))
			gossiper := &msgGossiper{}
			assert.NoError(t, gossiper.ensurePeerPartSetHeader(n.commit.BlockID.PartSetHeader, prs.ProposalBlockPartSetHeader))
			assert.True(t, n.parts.BitArray().Sub(prs.ProposalBlockParts).IsFull())
		})
	}
}

func TestPendingCommitAppliesRetainedFutureRoundBlock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := configSetup(t)
	cfg.Consensus.DontAutoPropose = true
	n := newCommitFixture(ctx, t, cfg, types.BlockPartSizeBytes, 2)
	stateData := n.node.GetStateData()
	stateData.ProposalBlock = n.block
	stateData.ProposalBlockParts = n.parts
	stateData.updateRoundStep(0, cstypes.RoundStepPrevote)
	ctx = dash.ContextWithProTxHash(ctx, n.node.privValidator.ProTxHash)
	ctx = msgInfoWithCtx(ctx, msgInfo{Msg: &CommitMessage{n.commit}, PeerID: n.peerID})
	require.NoError(t, n.node.ctrl.Dispatch(ctx,
		&TryAddCommitEvent{Commit: n.commit, PeerID: n.peerID}, &stateData))
	assert.Equal(t, n.block.Height+1, stateData.Height)
}
