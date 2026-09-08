package consensus

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/abci/example/kvstore"
	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sf "github.com/dashpay/tenderdash/internal/state/test/factory"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	"github.com/dashpay/tenderdash/types"
)

func TestSecurityProposalMetadataRelay(t *testing.T) {
	for _, field := range []string{"control", "core_height", "state_id"} {
		t.Run(field, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			css := makeConsensusState(ctx, t, configSetup(t), 2, "security-proposal-metadata", newTickerFunc())
			node := css[1]
			ctx = dash.ContextWithProTxHash(ctx, node.privValidator.ProTxHash)
			sd := node.GetStateData()
			sd.updateRoundStep(0, cstypes.RoundStepPropose)
			block, err := sf.MakeBlock(sd.state, 1, &types.Commit{}, kvstore.ProtocolVersion)
			require.NoError(t, err)
			block.CoreChainLockedHeight = 1
			parts, err := block.MakePartSet(types.BlockPartSizeBytes)
			require.NoError(t, err)
			proposal := types.NewProposal(block.Height, block.CoreChainLockedHeight, 0, -1, block.BlockID(parts), block.Time)
			proposer, err := sd.ProposerSelector.GetProposer(sd.Height, sd.Round)
			require.NoError(t, err)
			pp := proposal.ToProto()
			for _, cs := range css {
				if cs.privValidator.ProTxHash.Equal(proposer.ProTxHash) {
					_, err = cs.privValidator.SignProposal(ctx, sd.state.ChainID, sd.Validators.QuorumType, sd.Validators.QuorumHash, pp)
					require.NoError(t, err)
				}
			}
			proposal.Signature = pp.Signature
			honest, err := types.ProposalFromProto(proposal.ToProto())
			require.NoError(t, err)
			if field == "core_height" {
				proposal.CoreChainLockedHeight++
			}
			if field == "state_id" {
				proposal.BlockID.StateID = append([]byte(nil), proposal.BlockID.StateID...)
				proposal.BlockID.StateID[0] ^= 0xff
			}
			require.NoError(t, proposal.ValidateBasic())
			digest := types.ProposalBlockSignID(sd.state.ChainID, proposal.ToProto(), sd.Validators.QuorumType, sd.Validators.QuorumHash)
			require.True(t, proposer.PubKey.VerifySignatureDigest(digest, proposal.Signature), "relay mutation retains original proposer signature")
			deliver := func(msg Message) {
				t.Helper()
				require.NoError(t, node.msgDispatcher.dispatch(ctx, &sd, msgInfo{Msg: msg, PeerID: types.NodeID("relay"), ReceiveTime: tmtime.Now()}))
			}
			deliver(&ProposalMessage{Proposal: proposal})
			require.NotNil(t, sd.Proposal)
			// The honest peer delivers its authentic copy while the forged metadata is held.
			deliver(&ProposalMessage{Proposal: honest})
			for i := 0; i < int(parts.Total()); i++ {
				deliver(&BlockPartMessage{Height: block.Height, Round: 0, Part: parts.GetPart(i)})
			}
			require.NotNil(t, sd.Proposal, "an unsigned metadata mutation must not erase the honest proposal")
			require.Equal(t, honest.CoreChainLockedHeight, sd.Proposal.CoreChainLockedHeight,
				"consensus must retain the proposer's authenticated core height")
			require.True(t, honest.BlockID.Equals(sd.Proposal.BlockID),
				"consensus must retain the proposer's authenticated full block ID")
			require.Equal(t, cstypes.RoundStepPrevote, sd.Step)
		})
	}
}
