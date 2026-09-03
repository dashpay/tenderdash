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
	"github.com/dashpay/tenderdash/types"
)

// TestAddProposalBlockPartCompletesBlockParkedCommitWaitsFor covers a commit that
// arrives before the last part of the block it commits, while a Proposal for a
// block the network dropped is still around: the +2/3 prevote majority that
// retargeted ProposalBlockParts to the committed block left the Proposal untouched
// (addVoteUpdateValidBlockMw). The completing part is then checked against that
// Proposal's core chain locked height and rejected, and since a part set only
// completes once, nothing re-enters the path — the parked StateData.Commit turns
// every further commit into a no-op and the node stalls at this height
// (dashpay/tenderdash#1414).
func TestAddProposalBlockPartCompletesBlockParkedCommitWaitsFor(t *testing.T) {
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
	// A part size small enough that the block is gossiped in several parts, so a
	// commit can arrive while parts are still missing.
	parts, err := block.MakePartSet(64)
	require.NoError(t, err)
	require.Greater(t, parts.Total(), uint32(1), "a single-part block cannot arrive after its commit")

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

	// A proposal for a block the network dropped, disagreeing with the committed
	// block on the core chain locked height the completing part is checked against.
	staleProposal := types.NewProposal(
		block.Height,
		block.CoreChainLockedHeight+1,
		0,
		-1,
		factory.MakeBlockID(),
		block.Time,
	)

	received := types.NewPartSetFromHeader(commit.BlockID.PartSetHeader)
	added, err := received.AddPart(parts.GetPart(0))
	require.NoError(t, err)
	require.True(t, added)

	peerID := proposerStateData.Validators.Proposer().NodeAddress.NodeID
	stateData.Proposal = staleProposal
	stateData.ProposalBlockParts = received
	stateData.updateRoundStep(commit.Round, cstypes.RoundStepPrevote)

	ctx = dash.ContextWithProTxHash(ctx, otherNode.privValidator.ProTxHash)
	commitCtx := msgInfoWithCtx(ctx, msgInfo{Msg: &CommitMessage{commit}, PeerID: peerID})
	require.NoError(t, otherNode.ctrl.Dispatch(commitCtx, &TryAddCommitEvent{Commit: commit, PeerID: peerID}, &stateData))
	require.NotNil(t, stateData.Commit, "the commit must be kept until the block it commits arrives")
	assert.Nil(t, stateData.Proposal, "a proposal that outlived its own block must not gate the committed block")
	assert.Equal(t, uint32(1), stateData.ProposalBlockParts.Count(), "parts already collected for the committed block must be kept")

	for i := 1; i < int(parts.Total()); i++ {
		msg := &BlockPartMessage{Height: block.Height, Round: commit.Round, Part: parts.GetPart(i)}
		partCtx := msgInfoWithCtx(ctx, msgInfo{Msg: msg, PeerID: peerID})
		require.NoError(t,
			otherNode.ctrl.Dispatch(partCtx, &AddProposalBlockPartEvent{Msg: msg, PeerID: peerID}, &stateData),
			"block part %d of %d", i, parts.Total())
	}

	assert.Equal(t, int64(2), stateData.Height,
		"the block the parked commit was waiting for must be applied once its last part arrives")
}
