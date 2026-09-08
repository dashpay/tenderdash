package consensus

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/abci/example/kvstore"
	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sf "github.com/dashpay/tenderdash/internal/state/test/factory"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	"github.com/dashpay/tenderdash/types"
)

func TestCommitAfterDroppedProposalAppliesCompleteBlock(t *testing.T) {
	chainID := t.Name()
	for _, tc := range []struct {
		name            string
		commitFirst     bool
		localRound      int32
		step            cstypes.RoundStepType
		stateIDMismatch bool
		differentBlock  bool
		invalidCommit   string
	}{
		{name: "commit before block control", commitFirst: true, step: cstypes.RoundStepPrevote},
		{name: "block before current round commit", step: cstypes.RoundStepPrevote},
		{name: "block before commit during propose", step: cstypes.RoundStepPropose},
		{name: "block before earlier round commit", localRound: 1, step: cstypes.RoundStepPrevote},
		{name: "different block before current round commit", differentBlock: true, step: cstypes.RoundStepPrevote},
		{name: "different block before earlier round commit", differentBlock: true, localRound: 1, step: cstypes.RoundStepPrevote},
		{name: "different block before commit during propose", differentBlock: true, step: cstypes.RoundStepPropose},
		{name: "different block with invalid commit signature", differentBlock: true, invalidCommit: "signature", step: cstypes.RoundStepPrevote},
		{name: "state ID mismatch before current round commit", stateIDMismatch: true, step: cstypes.RoundStepPrevote},
		{name: "state ID mismatch before earlier round commit", localRound: 1, stateIDMismatch: true, step: cstypes.RoundStepPrevote},
		{name: "invalid commit signature", invalidCommit: "signature", step: cstypes.RoundStepPrevote},
		{name: "commit with different state ID", invalidCommit: "state_id", step: cstypes.RoundStepPrevote},
		{name: "commit with different block hash", invalidCommit: "hash", step: cstypes.RoundStepPrevote},
		{name: "commit with different part set", invalidCommit: "parts", step: cstypes.RoundStepPrevote},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			css := makeConsensusState(ctx, t, configSetup(t), 2, chainID, newTickerFunc())
			node := css[1]
			ctx = dash.ContextWithProTxHash(ctx, node.privValidator.ProTxHash)
			source, stateData := css[0].GetStateData(), node.GetStateData()
			privVals := []types.PrivValidator{css[0].privValidator.PrivValidator, css[1].privValidator.PrivValidator}
			block, err := sf.MakeBlock(source.state, 1, &types.Commit{}, kvstore.ProtocolVersion)
			require.NoError(t, err)
			block.CoreChainLockedHeight = 1
			if tc.differentBlock {
				block.CoreChainLockedHeight = 2
			}
			parts, err := block.MakePartSet(types.BlockPartSizeBytes)
			require.NoError(t, err)
			committedParts := parts
			committedBlock := block
			if tc.differentBlock {
				committedBlock, err = sf.MakeBlock(source.state, 1, &types.Commit{}, kvstore.ProtocolVersion)
				require.NoError(t, err)
				committedBlock.CoreChainLockedHeight = 1
				committedParts, err = committedBlock.MakePartSet(types.BlockPartSizeBytes)
				require.NoError(t, err)
				require.False(t, parts.HasHeader(committedParts.Header()))
			}
			commitBlockID := committedBlock.BlockID(committedParts)
			switch tc.invalidCommit {
			case "hash", "state_id":
				commitBlockID = forgeField(t, commitBlockID, tc.invalidCommit)
			case "parts":
				commitBlockID.PartSetHeader.Total++
			}
			commit, err := factory.MakeCommit(ctx, commitBlockID, block.Height, 0,
				source.Votes.Precommits(0), source.Validators, privVals)
			require.NoError(t, err)
			require.NoError(t, stateData.Validators.VerifyCommit(stateData.state.ChainID,
				commitBlockID, block.Height, commit))
			if tc.invalidCommit == "signature" {
				commit.ThresholdBlockSignature[0] ^= 0xFF
			}
			stateData.updateRoundStep(tc.localRound, tc.step)
			proposer, err := stateData.ProposerSelector.GetProposer(block.Height, tc.localRound)
			require.NoError(t, err)
			var proposerKey types.PrivValidator
			for _, pv := range privVals {
				id, err := pv.GetProTxHash(ctx)
				require.NoError(t, err)
				if id.Equal(proposer.ProTxHash) {
					proposerKey = pv
				}
			}
			require.NotNil(t, proposerKey)
			proposal := types.NewProposal(block.Height, block.CoreChainLockedHeight+1,
				tc.localRound, -1, block.BlockID(parts), block.Time)
			if tc.stateIDMismatch {
				proposal.CoreChainLockedHeight = block.CoreChainLockedHeight
				proposal.BlockID = forgeField(t, block.BlockID(parts), "state_id")
			}
			protoProposal := proposal.ToProto()
			_, err = proposerKey.SignProposal(ctx, stateData.state.ChainID,
				stateData.Validators.QuorumType, stateData.Validators.QuorumHash, protoProposal)
			require.NoError(t, err)
			proposal.Signature = protoProposal.Signature
			peerID := types.NodeID("peer")
			deliver := func(msg Message) {
				t.Helper()
				require.NoError(t, node.msgDispatcher.dispatch(ctx, &stateData,
					msgInfo{Msg: msg, PeerID: peerID, ReceiveTime: tmtime.Now()}))
			}
			if tc.commitFirst {
				deliver(&CommitMessage{Commit: commit})
				require.NotNil(t, stateData.Commit)
			}
			deliver(&ProposalMessage{Proposal: proposal})
			require.NotNil(t, stateData.Proposal, "the signed proposal must enter via its real handler")
			for i := 0; i < int(parts.Total()); i++ {
				deliver(&BlockPartMessage{Height: block.Height, Round: tc.localRound, Part: parts.GetPart(i)})
			}
			if !tc.commitFirst {
				require.NotNil(t, stateData.Proposal, "the completing part must preserve the signed proposal")
				require.Equal(t, block.CoreChainLockedHeight, stateData.Proposal.CoreChainLockedHeight)
				require.True(t, stateData.Proposal.BlockID.Equals(block.BlockID(parts)))
				require.True(t, stateData.ProposalBlockParts.IsComplete())
				require.True(t, stateData.ProposalBlock.BlockID(stateData.ProposalBlockParts).Equals(block.BlockID(parts)))
				if tc.invalidCommit != "" {
					deliver(&CommitMessage{Commit: commit})
					assert.Equal(t, block.Height, stateData.Height)
					if tc.invalidCommit == "parts" {
						// An authenticated unknown part set selects a download, not the retained block.
						assert.Equal(t, commit, stateData.Commit)
						assert.False(t, stateData.ProposalBlockParts.IsComplete())
						assert.True(t, stateData.ProposalBlockParts.HasHeader(commit.BlockID.PartSetHeader))
						return
					}
					assert.Nil(t, stateData.Commit)
					assert.True(t, stateData.ProposalBlockParts.IsComplete())
					assert.True(t, stateData.ProposalBlockParts.HasHeader(parts.Header()))
					return
				}
				deliver(&CommitMessage{Commit: commit})
				if tc.differentBlock {
					require.Equal(t, block.Height, stateData.Height, "the retained block must not be applied")
					require.Equal(t, commit, stateData.Commit)
					require.False(t, stateData.ProposalBlockParts.IsComplete())
					require.True(t, stateData.ProposalBlockParts.HasHeader(committedParts.Header()))
					for i := 0; i < int(committedParts.Total()); i++ {
						deliver(&BlockPartMessage{Height: block.Height, Round: tc.localRound, Part: committedParts.GetPart(i)})
					}
				}
			}
			assert.Equal(t, block.Height+1, stateData.Height,
				"the valid committed block must be applied regardless of arrival order")
			assert.True(t, stateData.state.LastBlockID.Equals(commit.BlockID))
		})
	}
}
