package consensus

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	abcimocks "github.com/dashpay/tenderdash/abci/types/mocks"
	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/types"
)

// blocksNotFollowingState corrupts, one per case, a header field that only the
// committed state can tell is wrong.
var blocksNotFollowingState = []struct {
	name   string
	mutate func(*types.Block)
}{
	{"height", func(b *types.Block) { b.Height++ }},
	{"chain ID", func(b *types.Block) { b.ChainID = "other-chain" }},
	{"last block ID", func(b *types.Block) { b.LastBlockID = factory.MakeBlockID() }},
}

// newNodeNotProposing returns a validator node at the initial height that is
// not round 0's proposer, the proposer's key, and the node's application, which
// fails ProcessProposal as an application may for a block it cannot process.
func newNodeNotProposing(ctx context.Context, t *testing.T) (*State, types.PrivValidator, *abcimocks.Application) {
	t.Helper()
	app := abcimocks.NewApplication(t)
	app.On("ProcessProposal", mock.Anything, mock.Anything).
		Return(nil, errors.New("application cannot process this block")).Maybe()

	genDoc, privVals := factory.RandGenesisDoc(2, factory.ConsensusParams())
	state, err := sm.MakeGenesisState(genDoc)
	require.NoError(t, err)
	cs := newStateWithConfig(ctx, t, consensusLogger(t), configSetup(t), state, privVals[0], app)

	sd := cs.GetStateData()
	proposer, err := sd.ProposerSelector.GetProposer(sd.Height, 0)
	require.NoError(t, err)
	if proposer.ProTxHash.Equal(cs.privValidator.ProTxHash) {
		cs.SetPrivValidator(ctx, privVals[1])
		return cs, privVals[0], app
	}
	return cs, privVals[1], app
}

// proposalNotFollowingState returns round 0's proposal, signed by proposer, of a
// block that mutate makes not follow the node's committed state.
func proposalNotFollowingState(
	ctx context.Context,
	t *testing.T,
	cs *State,
	proposer types.PrivValidator,
	mutate func(*types.Block),
) (*types.Proposal, *types.PartSet) {
	t.Helper()
	sd := cs.GetStateData()
	proTxHash, err := proposer.GetProTxHash(ctx)
	require.NoError(t, err)

	block := sd.state.MakeBlock(sd.Height, nil, types.NewCommit(0, 0, types.BlockID{}, nil, nil), nil, proTxHash, 1)
	block.CoreChainLockedHeight = sd.state.LastCoreChainLockedBlockHeight
	mutate(block)
	parts, err := block.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)

	proposal := types.NewProposal(sd.Height, block.CoreChainLockedHeight, 0, -1, block.BlockID(parts), block.Time)
	p := proposal.ToProto()
	_, err = proposer.SignProposal(ctx, sd.state.ChainID, sd.Validators.QuorumType, sd.Validators.QuorumHash, p)
	require.NoError(t, err)
	proposal.Signature = p.Signature
	return proposal, parts
}

// TestStatePrevotesNilOnBlockNotFollowingState checks that a validator prevotes
// nil, without asking the application, on a proposed block whose header does
// not follow its committed state.
func TestStatePrevotesNilOnBlockNotFollowingState(t *testing.T) {
	for _, tc := range blocksNotFollowingState {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			cs, proposer, app := newNodeNotProposing(ctx, t)
			proposal, parts := proposalNotFollowingState(ctx, t, cs, proposer, tc.mutate)
			voteCh := subscribeToVoter(ctx, t, cs, cs.privValidator.ProTxHash)

			require.NoError(t, cs.SetProposalAndBlock(ctx, proposal, parts, "peer"))
			startTestRound(ctx, cs, proposal.Height, 0)

			ensurePrevoteMatch(t, voteCh, proposal.Height, 0, nil)
			app.AssertNotCalled(t, "ProcessProposal", mock.Anything, mock.Anything)
		})
	}
}

// TestReplayOfBlockNotFollowingStatePrevotesNil checks that replaying the WAL
// of a node that received a proposed block not following its committed state
// prevotes nil again without asking the application, and the node keeps running.
func TestReplayOfBlockNotFollowingStatePrevotesNil(t *testing.T) {
	for _, tc := range blocksNotFollowingState {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			cs, proposer, app := newNodeNotProposing(ctx, t)
			proposal, parts := proposalNotFollowingState(ctx, t, cs, proposer, tc.mutate)
			wal := []msgInfo{{Msg: &ProposalMessage{Proposal: proposal}, PeerID: "peer"}}
			for i := 0; i < int(parts.Total()); i++ {
				wal = append(wal, msgInfo{
					Msg:    &BlockPartMessage{Height: proposal.Height, Round: 0, Part: parts.GetPart(i)},
					PeerID: "peer",
				})
			}

			cs.emitter.Emit(setReplayModeEventName, true)
			ctx = dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash)
			require.NotPanics(t, func() {
				for _, msg := range wal {
					assert.NoError(t, cs.readReplayMessage(ctx, &TimedWALMessage{Msg: msg}, nil))
				}
			})

			sd := cs.GetStateData()
			assert.Equal(t, cstypes.RoundStepPrevote, sd.Step)
			app.AssertNotCalled(t, "ProcessProposal", mock.Anything, mock.Anything)
		})
	}
}
