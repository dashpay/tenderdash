package consensus

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/abci/example/kvstore"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sf "github.com/dashpay/tenderdash/internal/state/test/factory"
	"github.com/dashpay/tenderdash/libs/log"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// verifyExtensionContractApp accepts a vote extension only for a (height,
// round, block hash) it ran ProcessProposal for, as Dash Drive does: it signs
// withdrawal transactions while processing a proposal and can verify the
// extension of a precommit for that block and round alone.
type verifyExtensionContractApp struct {
	abci.BaseApplication

	// rejectProposals makes ProcessProposal reject every block.
	rejectProposals bool

	mtx       sync.Mutex
	calls     int // ProcessProposal calls, accepted or not
	processed []processedProposal
	accepted  int
	rejected  int
}

func (app *verifyExtensionContractApp) ProcessProposal(
	ctx context.Context,
	req *abci.RequestProcessProposal,
) (*abci.ResponseProcessProposal, error) {
	app.mtx.Lock()
	defer app.mtx.Unlock()
	app.calls++
	if app.rejectProposals {
		return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_REJECT}, nil
	}
	app.processed = append(app.processed, processedProposal{height: req.Height, round: req.Round, hash: bytes.Clone(req.Hash)})
	return app.BaseApplication.ProcessProposal(ctx, req)
}

func (app *verifyExtensionContractApp) VerifyVoteExtension(
	_ context.Context,
	req *abci.RequestVerifyVoteExtension,
) (*abci.ResponseVerifyVoteExtension, error) {
	app.mtx.Lock()
	defer app.mtx.Unlock()
	for _, p := range app.processed {
		if p.height == req.Height && p.round == req.Round && bytes.Equal(p.hash, req.Hash) {
			app.accepted++
			return &abci.ResponseVerifyVoteExtension{Status: abci.ResponseVerifyVoteExtension_ACCEPT}, nil
		}
	}
	app.rejected++
	return &abci.ResponseVerifyVoteExtension{Status: abci.ResponseVerifyVoteExtension_REJECT}, nil
}

// counts returns the ProcessProposal calls and the extensions accepted and
// rejected so far.
func (app *verifyExtensionContractApp) counts() (calls, accepted, rejected int) {
	app.mtx.Lock()
	defer app.mtx.Unlock()
	return app.calls, app.accepted, app.rejected
}

// round1Node is a validator in round 1 of height 1, driven through the real
// message path, with a block of that round's proposer that its three peers
// hold and it does not until deliverParts.
type round1Node struct {
	ctx     context.Context
	cs      *State
	sd      StateData
	app     *verifyExtensionContractApp
	self    *validatorStub
	peers   []*validatorStub
	block   *types.Block
	parts   *types.PartSet
	blockID types.BlockID
}

func newRound1Node(ctx context.Context, t *testing.T, app *verifyExtensionContractApp, step cstypes.RoundStepType) *round1Node {
	t.Helper()
	cs, vss := makeState(ctx, t, makeStateArgs{application: app, logger: log.NewNopLogger()})
	ctx = dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash)
	n := &round1Node{ctx: ctx, cs: cs, app: app, sd: cs.GetStateData()}
	sd := &n.sd

	var err error
	n.block, err = sf.MakeBlock(sd.state, 1, &types.Commit{}, kvstore.ProtocolVersion)
	require.NoError(t, err)
	n.block.CoreChainLockedHeight = sd.state.LastCoreChainLockedBlockHeight
	proposer, err := sd.ProposerSelector.GetProposer(1, 1)
	require.NoError(t, err)
	n.block.ProposerProTxHash = proposer.ProTxHash
	n.parts, err = n.block.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)
	n.blockID = n.block.BlockID(n.parts)

	sd.Votes.SetRound(2)
	sd.updateRoundStep(1, step)
	for _, vs := range vss {
		vs.Round = 1
		proTxHash, err := vs.GetProTxHash(ctx)
		require.NoError(t, err)
		if proTxHash.Equal(cs.privValidator.ProTxHash) {
			n.self = vs
		} else {
			n.peers = append(n.peers, vs)
		}
	}
	require.NotNil(t, n.self)
	require.Len(t, n.peers, 3)
	return n
}

func (n *round1Node) deliver(t *testing.T, msg Message) {
	t.Helper()
	peer := types.NodeID("aa00000000000000000000000000000000000000")
	require.NoError(t, n.cs.msgDispatcher.dispatch(n.ctx, &n.sd, msgInfo{Msg: msg, PeerID: peer, ReceiveTime: tmtime.Now()}))
}

func (n *round1Node) vote(t *testing.T, vs *validatorStub, voteType tmproto.SignedMsgType, blockID types.BlockID) *types.Vote {
	t.Helper()
	return signVote(n.ctx, t, vs, voteType, n.sd.state.ChainID, blockID, n.sd.Validators.QuorumType, n.sd.Validators.QuorumHash)
}

// deliverVotes delivers one vote of voteType for blockID from each of vss.
func (n *round1Node) deliverVotes(t *testing.T, voteType tmproto.SignedMsgType, blockID types.BlockID, vss ...*validatorStub) []*types.Vote {
	t.Helper()
	votes := make([]*types.Vote, 0, len(vss))
	for _, vs := range vss {
		v := n.vote(t, vs, voteType, blockID)
		votes = append(votes, v)
		n.deliver(t, &VoteMessage{Vote: v})
	}
	return votes
}

// deliverProposal delivers the round's proposal for the block, signed by the
// round's proposer.
func (n *round1Node) deliverProposal(t *testing.T) {
	t.Helper()
	proposer, err := n.sd.ProposerSelector.GetProposer(1, 1)
	require.NoError(t, err)
	var pv types.PrivValidator
	for _, vs := range append([]*validatorStub{n.self}, n.peers...) {
		if proTxHash, _ := vs.GetProTxHash(n.ctx); proTxHash.Equal(proposer.ProTxHash) {
			pv = vs.PrivValidator
		}
	}
	require.NotNil(t, pv)
	proposal := types.NewProposal(1, n.block.CoreChainLockedHeight, 1, -1, n.blockID, n.block.Time)
	pp := proposal.ToProto()
	_, err = pv.SignProposal(n.ctx, n.sd.state.ChainID, n.sd.Validators.QuorumType, n.sd.Validators.QuorumHash, pp)
	require.NoError(t, err)
	proposal.Signature = pp.Signature
	n.deliver(t, &ProposalMessage{Proposal: proposal})
	require.NotNil(t, n.sd.Proposal)
}

func (n *round1Node) deliverParts(t *testing.T) {
	t.Helper()
	for i := uint32(0); i < n.parts.Total(); i++ {
		n.deliver(t, &BlockPartMessage{Height: 1, Round: 1, Part: n.parts.GetPart(int(i))})
	}
}

// polkaWithoutBlock takes the node, in the prevote step having prevoted nil,
// through its peers' polka for the block it does not hold: it waits for the
// block, as its part set is retargeted at it.
func (n *round1Node) polkaWithoutBlock(t *testing.T) {
	t.Helper()
	n.deliverVotes(t, tmproto.PrevoteType, types.BlockID{}, n.self)
	n.deliverVotes(t, tmproto.PrevoteType, n.blockID, n.peers...)
	require.Equal(t, cstypes.RoundStepPrevoteWait, n.sd.Step, "polka for a block not held waits for it")
	require.True(t, n.sd.ProposalBlockParts.HasHeader(n.blockID.PartSetHeader), "polka retargets the part set")
}

// timeoutPrevote is the prevote-wait timeout: the node precommits nil.
func (n *round1Node) timeoutPrevote(t *testing.T) {
	t.Helper()
	require.NoError(t, n.cs.ctrl.Dispatch(n.ctx, &EnterPrecommitEvent{Height: 1, Round: 1}, &n.sd))
	require.Equal(t, cstypes.RoundStepPrecommit, n.sd.Step)
}

// newLaggingPolkaNode is a validator that, in round 1 of height 1, saw a polka
// for a block it never received, precommitted nil on the prevote-wait timeout
// and rejected the non-nil precommits of the two peers that had the block,
// which it returns.
func newLaggingPolkaNode(ctx context.Context, t *testing.T, app *verifyExtensionContractApp) (*round1Node, []*types.Vote) {
	t.Helper()
	n := newRound1Node(ctx, t, app, cstypes.RoundStepPrevote)
	n.polkaWithoutBlock(t)
	n.timeoutPrevote(t)

	// Own and one peer's nil precommit are stored; the two for the block are
	// rejected, as the application has not processed it.
	n.deliverVotes(t, tmproto.PrecommitType, types.BlockID{}, n.self, n.peers[2])
	precommits := n.deliverVotes(t, tmproto.PrecommitType, n.blockID, n.peers[:2]...)
	calls, accepted, rejected := app.counts()
	require.Equal(t, [3]int{0, 0, 2}, [3]int{calls, accepted, rejected})
	require.Len(t, n.sd.Votes.Precommits(1).List(), 2)
	require.False(t, n.sd.Votes.Precommits(1).HasTwoThirdsAny())
	require.False(t, n.sd.TriggeredTimeoutPrecommit)
	return n, precommits
}

// A polka block that arrives in the precommit step is processed for the round
// so that the application can verify the extensions of the precommits for it,
// without this node voting again.
func TestStatePolkaBlockCompletedInPrecommitIsProcessed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n, precommits := newLaggingPolkaNode(ctx, t, &verifyExtensionContractApp{})

	n.deliverParts(t)

	require.True(t, n.sd.ProposalBlock.HashesTo(n.blockID.Hash))
	calls, _, _ := n.app.counts()
	require.Equal(t, 1, calls, "the completed polka block is processed once")
	require.True(t, processedFor(&n.sd.RoundState, 1))
	require.Equal(t, cstypes.RoundStepPrecommit, n.sd.Step, "no vote is cast again")
	require.Len(t, n.sd.Votes.Prevotes(1).List(), 4)

	// A peer's precommit for the block, delivered again, is now accepted.
	n.deliver(t, &VoteMessage{Vote: precommits[0]})
	calls, accepted, _ := n.app.counts()
	require.Equal(t, 1, calls, "a precommit does not process the block again")
	require.Equal(t, 1, accepted)
	require.Len(t, n.sd.Votes.Precommits(1).List(), 3)
}

// Once the polka block and the rejected precommits for it are delivered, the
// lagging node holds +2/3 precommits of any kind and starts the precommit wait
// that takes it to the next round.
func TestStatePolkaBlockDeliveryEndsThePrecommitStall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n, precommits := newLaggingPolkaNode(ctx, t, &verifyExtensionContractApp{})

	n.deliverParts(t)
	for _, v := range precommits {
		n.deliver(t, &VoteMessage{Vote: v})
	}

	_, accepted, _ := n.app.counts()
	require.Equal(t, 2, accepted)
	require.True(t, n.sd.Votes.Precommits(1).HasTwoThirdsAny())
	require.True(t, n.sd.TriggeredTimeoutPrecommit, "precommit wait starts once every precommit counts")
	require.Equal(t, int32(1), n.sd.Round)
}

// A polka block the application rejects is processed once: neither the
// precommits for it, which stay rejected, nor its parts arriving again process
// it anew.
func TestStatePolkaBlockRejectedByTheApplicationIsNotRetried(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n, precommits := newLaggingPolkaNode(ctx, t, &verifyExtensionContractApp{rejectProposals: true})

	n.deliverParts(t)
	calls, _, _ := n.app.counts()
	require.Equal(t, 1, calls)
	require.False(t, processedFor(&n.sd.RoundState, 1), "a rejected block does not count as processed")

	for _, v := range precommits {
		n.deliver(t, &VoteMessage{Vote: v})
	}
	n.deliverParts(t)
	calls, accepted, rejected := n.app.counts()
	require.Equal(t, [3]int{1, 0, 4}, [3]int{calls, accepted, rejected})
	require.Equal(t, cstypes.RoundStepPrecommit, n.sd.Step)
	require.False(t, n.sd.TriggeredTimeoutPrecommit)
}

// A polka block completing in the prevote wait is processed there, and the
// precommit step that follows, precommitting nil for want of the proposal, does
// not process it again.
func TestStatePolkaBlockCompletedInPrevoteWaitIsProcessedOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := newRound1Node(ctx, t, &verifyExtensionContractApp{}, cstypes.RoundStepPrevote)
	n.polkaWithoutBlock(t)

	n.deliverParts(t)
	calls, _, _ := n.app.counts()
	require.Equal(t, 1, calls)
	require.True(t, processedFor(&n.sd.RoundState, 1))
	require.Equal(t, cstypes.RoundStepPrevoteWait, n.sd.Step, "processing casts no vote")

	n.timeoutPrevote(t)
	calls, _, _ = n.app.counts()
	require.Equal(t, 1, calls)
	require.Equal(t, int32(-1), n.sd.LockedRound, "nil precommitted: no proposal was received")
	require.Equal(t, int32(1), n.sd.ValidRound)
}

// A proposal completing in the propose step after its polka takes the usual
// path: the node prevotes and precommits the block, processing it once.
func TestStatePolkaBlockCompletedInProposeVotesAndProcessesOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := newRound1Node(ctx, t, &verifyExtensionContractApp{}, cstypes.RoundStepPropose)
	n.deliverVotes(t, tmproto.PrevoteType, n.blockID, n.peers...)
	require.Equal(t, cstypes.RoundStepPropose, n.sd.Step)
	n.deliverProposal(t)

	n.deliverParts(t)
	calls, _, _ := n.app.counts()
	require.Equal(t, 1, calls)
	require.True(t, processedFor(&n.sd.RoundState, 1))
	require.Equal(t, cstypes.RoundStepPrecommit, n.sd.Step)
	require.Equal(t, int32(1), n.sd.LockedRound, "the block is precommitted")
	require.True(t, n.sd.LockedBlock.HashesTo(n.blockID.Hash))
}

// A block that completes in the precommit step without being this round's
// polka block is not processed: here the round's polka is for nil.
func TestStateCompletedBlockWithoutItsPolkaIsNotProcessed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := newRound1Node(ctx, t, &verifyExtensionContractApp{}, cstypes.RoundStepPrevote)
	n.deliverProposal(t)
	n.deliverVotes(t, tmproto.PrevoteType, types.BlockID{}, n.self)
	n.deliverVotes(t, tmproto.PrevoteType, types.BlockID{}, n.peers...)
	require.Equal(t, cstypes.RoundStepPrecommit, n.sd.Step, "+2/3 prevoted nil")

	n.deliverParts(t)
	require.True(t, n.sd.ProposalBlock.HashesTo(n.blockID.Hash))
	calls, _, _ := n.app.counts()
	require.Equal(t, 0, calls)
	require.False(t, processedFor(&n.sd.RoundState, 1))
	require.Equal(t, int32(-1), n.sd.ValidRound)
}
