package consensus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	abcimocks "github.com/dashpay/tenderdash/abci/types/mocks"
	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
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

// proposalOfMutatedBlock returns round 0's proposal, signed by proposer, of the
// block the node expects, altered by mutate.
func proposalOfMutatedBlock(
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
			proposal, parts := proposalOfMutatedBlock(ctx, t, cs, proposer, tc.mutate)
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
			proposal, parts := proposalOfMutatedBlock(ctx, t, cs, proposer, tc.mutate)
			replayProposal(ctx, t, cs, proposal, parts, tmtime.Now())

			requireOwnNilPrevote(ctx, t, cs)
			app.AssertNotCalled(t, "ProcessProposal", mock.Anything, mock.Anything)
		})
	}
}

// TestReplayOfUntimelyProposalPrevotesNil checks that replaying the WAL of a
// node that received an untimely proposal prevotes nil without asking the
// application, as the node did when it received it, and dates the proposal by
// its WAL entry.
func TestReplayOfUntimelyProposalPrevotesNil(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cs, proposer, app := newNodeNotProposing(ctx, t)
	proposal, parts := proposalOfMutatedBlock(ctx, t, cs, proposer, func(b *types.Block) {
		b.Time = b.Time.AddDate(100, 0, 0)
	})
	walTime := tmtime.Now()
	replayProposal(ctx, t, cs, proposal, parts, walTime)

	requireOwnNilPrevote(ctx, t, cs)
	assert.True(t, walTime.Equal(cs.GetStateData().ProposalReceiveTime), "the WAL time dates the proposal")
	app.AssertNotCalled(t, "ProcessProposal", mock.Anything, mock.Anything)
}

// requireOwnNilPrevote checks that cs, not running, is at the prevote step and
// has queued its own prevote, for nil.
func requireOwnNilPrevote(ctx context.Context, t *testing.T, cs *State) {
	t.Helper()
	require.Equal(t, cstypes.RoundStepPrevote, cs.GetStateData().Step)
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	for {
		mi, ok := cs.msgInfoQueue.sender.internalQueue.recv(ctx)
		require.True(t, ok, "the node cast no prevote")
		if msg, isVote := mi.Msg.(*VoteMessage); isVote && msg.Vote.Type == tmproto.PrevoteType {
			assert.True(t, msg.Vote.BlockID.IsNil(), "the node prevoted for block %v", msg.Vote.BlockID)
			return
		}
	}
}

// replayProposal replays, in replay mode, the WAL entries of proposal and its
// parts as written at walTime by a node that received them from a peer.
func replayProposal(
	ctx context.Context,
	t *testing.T,
	cs *State,
	proposal *types.Proposal,
	parts *types.PartSet,
	walTime time.Time,
) {
	t.Helper()
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
			assert.NoError(t, cs.readReplayMessage(ctx, &TimedWALMessage{Time: walTime, Msg: msg}, nil))
		}
	})
}
