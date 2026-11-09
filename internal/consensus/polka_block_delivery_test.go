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

	mtx       sync.Mutex
	processed []processedProposal
	accepted  int
	rejected  int
}

func (app *verifyExtensionContractApp) ProcessProposal(
	ctx context.Context,
	req *abci.RequestProcessProposal,
) (*abci.ResponseProcessProposal, error) {
	app.mtx.Lock()
	app.processed = append(app.processed, processedProposal{height: req.Height, round: req.Round, hash: bytes.Clone(req.Hash)})
	app.mtx.Unlock()
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

func (app *verifyExtensionContractApp) counts() (processed, accepted, rejected int) {
	app.mtx.Lock()
	defer app.mtx.Unlock()
	return len(app.processed), app.accepted, app.rejected
}

// laggingPolkaNode is a validator that, in round 1 of height 1, saw a polka for
// a block it never received, precommitted nil on the prevote-wait timeout and
// rejected the non-nil precommits of the peers that had the block.
type laggingPolkaNode struct {
	ctx       context.Context
	cs        *State
	sd        StateData
	app       *verifyExtensionContractApp
	parts     *types.PartSet
	blockID   types.BlockID
	precommit []*types.Vote // the peers' precommits for the block, rejected once
}

func (n *laggingPolkaNode) deliver(t *testing.T, msg Message) {
	t.Helper()
	peer := types.NodeID("aa00000000000000000000000000000000000000")
	require.NoError(t, n.cs.msgDispatcher.dispatch(n.ctx, &n.sd, msgInfo{Msg: msg, PeerID: peer, ReceiveTime: tmtime.Now()}))
}

func (n *laggingPolkaNode) deliverParts(t *testing.T) {
	t.Helper()
	for i := uint32(0); i < n.parts.Total(); i++ {
		n.deliver(t, &BlockPartMessage{Height: 1, Round: 1, Part: n.parts.GetPart(int(i))})
	}
}

func newLaggingPolkaNode(ctx context.Context, t *testing.T) *laggingPolkaNode {
	t.Helper()
	app := &verifyExtensionContractApp{}
	cs, vss := makeState(ctx, t, makeStateArgs{application: app, logger: log.NewNopLogger()})
	ctx = dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash)
	n := &laggingPolkaNode{ctx: ctx, cs: cs, app: app, sd: cs.GetStateData()}
	sd := &n.sd

	block, err := sf.MakeBlock(sd.state, 1, &types.Commit{}, kvstore.ProtocolVersion)
	require.NoError(t, err)
	block.CoreChainLockedHeight = sd.state.LastCoreChainLockedBlockHeight
	n.parts, err = block.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)
	n.blockID = block.BlockID(n.parts)

	// Round 1, prevote step: this node prevoted nil on the propose timeout.
	sd.Votes.SetRound(2)
	sd.updateRoundStep(1, cstypes.RoundStepPrevote)
	var self *validatorStub
	var peers []*validatorStub
	for _, vs := range vss {
		vs.Round = 1
		proTxHash, err := vs.GetProTxHash(ctx)
		require.NoError(t, err)
		if proTxHash.Equal(cs.privValidator.ProTxHash) {
			self = vs
		} else {
			peers = append(peers, vs)
		}
	}
	require.NotNil(t, self)
	require.Len(t, peers, 3)
	withBlock, nilVoter := peers[:2], peers[2]

	qt, qh, chainID := sd.Validators.QuorumType, sd.Validators.QuorumHash, sd.state.ChainID
	vote := func(vs *validatorStub, voteType tmproto.SignedMsgType, blockID types.BlockID) *types.Vote {
		return signVote(ctx, t, vs, voteType, chainID, blockID, qt, qh)
	}

	n.deliver(t, &VoteMessage{Vote: vote(self, tmproto.PrevoteType, types.BlockID{})})
	for _, vs := range peers {
		n.deliver(t, &VoteMessage{Vote: vote(vs, tmproto.PrevoteType, n.blockID)})
	}
	require.Equal(t, cstypes.RoundStepPrevoteWait, sd.Step, "polka for a block not held waits for it")
	require.True(t, sd.ProposalBlockParts.HasHeader(n.blockID.PartSetHeader), "polka retargets the part set")
	// The prevote-wait timeout.
	require.NoError(t, cs.ctrl.Dispatch(ctx, &EnterPrecommitEvent{Height: 1, Round: 1}, sd))
	require.Equal(t, cstypes.RoundStepPrecommit, sd.Step)

	// Own and one peer's nil precommit are stored; the two for the block are
	// rejected, as the application has not processed it.
	n.deliver(t, &VoteMessage{Vote: vote(self, tmproto.PrecommitType, types.BlockID{})})
	n.deliver(t, &VoteMessage{Vote: vote(nilVoter, tmproto.PrecommitType, types.BlockID{})})
	for _, vs := range withBlock {
		v := vote(vs, tmproto.PrecommitType, n.blockID)
		n.precommit = append(n.precommit, v)
		n.deliver(t, &VoteMessage{Vote: v})
	}
	processed, accepted, rejected := app.counts()
	require.Equal(t, [3]int{0, 0, 2}, [3]int{processed, accepted, rejected})
	require.Len(t, sd.Votes.Precommits(1).List(), 2)
	require.False(t, sd.Votes.Precommits(1).HasTwoThirdsAny())
	require.False(t, sd.TriggeredTimeoutPrecommit)
	return n
}

// A polka block that arrives in the precommit step is processed for the round
// so that the application can verify the extensions of the precommits for it,
// without this node voting again.
func TestStatePolkaBlockCompletedInPrecommitIsProcessed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := newLaggingPolkaNode(ctx, t)

	n.deliverParts(t)

	require.True(t, n.sd.ProposalBlock.HashesTo(n.blockID.Hash))
	processed, _, _ := n.app.counts()
	require.Equal(t, 1, processed, "the completed polka block is processed once")
	require.True(t, processedFor(&n.sd.RoundState, 1))
	require.Equal(t, cstypes.RoundStepPrecommit, n.sd.Step, "no vote is cast again")
	require.Len(t, n.sd.Votes.Prevotes(1).List(), 4)

	// A peer's precommit for the block, delivered again, is now accepted.
	n.deliver(t, &VoteMessage{Vote: n.precommit[0]})
	processed, accepted, _ := n.app.counts()
	require.Equal(t, 1, processed, "a precommit does not process the block again")
	require.Equal(t, 1, accepted)
	require.Len(t, n.sd.Votes.Precommits(1).List(), 3)
}

// Once the polka block and the rejected precommits for it are delivered, the
// lagging node holds +2/3 precommits of any kind and starts the precommit wait
// that takes it to the next round.
func TestStatePolkaBlockDeliveryEndsThePrecommitStall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := newLaggingPolkaNode(ctx, t)

	n.deliverParts(t)
	for _, v := range n.precommit {
		n.deliver(t, &VoteMessage{Vote: v})
	}

	_, accepted, _ := n.app.counts()
	require.Equal(t, 2, accepted)
	require.True(t, n.sd.Votes.Precommits(1).HasTwoThirdsAny())
	require.True(t, n.sd.TriggeredTimeoutPrecommit, "precommit wait starts once every precommit counts")
	require.Equal(t, int32(1), n.sd.Round)
}
