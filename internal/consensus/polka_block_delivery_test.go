package consensus

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/abci/example/kvstore"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sf "github.com/dashpay/tenderdash/internal/state/test/factory"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/log"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// verifyExtensionContractApp enforces the strict same-round contract some
// applications require: a vote extension is accepted only for a (height,
// round, block hash) accepted by ProcessProposal.
type verifyExtensionContractApp struct {
	abci.BaseApplication

	// rejectProposals makes ProcessProposal reject every block.
	rejectProposals bool
	// transientFailures is the number of initial calls that fail before acceptance.
	transientFailures int

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
	if app.calls <= app.transientFailures {
		return nil, errors.New("temporary ProcessProposal failure")
	}
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
	n.deliverProposalAt(t, n.block.Time)
}

// deliverProposalAt is deliverProposal with the proposal timestamped at ts.
func (n *round1Node) deliverProposalAt(t *testing.T, ts time.Time) {
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
	proposal := types.NewProposal(1, n.block.CoreChainLockedHeight, 1, -1, n.blockID, ts)
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

// startReceiveRoutine runs the node's consensus loop from n.sd until the
// returned function stops it. n.sd must not be used while it runs.
func (n *round1Node) startReceiveRoutine(t *testing.T) func() {
	t.Helper()
	require.NoError(t, n.sd.Save())
	ctx, cancel := context.WithCancel(n.ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		n.cs.receiveRoutine(ctx, nil)
	}()
	return func() {
		cancel()
		<-done
	}
}

// A polka block whose processing fails transiently is processed again by the
// consensus loop, with no other input, so that the precommits for it, resent,
// verify and start the precommit wait.
func TestStatePolkaBlockRetryEndsThePrecommitStall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n, precommits := newLaggingPolkaNode(ctx, t, &verifyExtensionContractApp{transientFailures: 1})
	n.deliverParts(t)
	calls, _, _ := n.app.counts()
	require.Equal(t, 1, calls)
	require.False(t, processedFor(&n.sd.RoundState, 1))
	require.Nil(t, n.sd.Proposal)

	stop := n.startReceiveRoutine(t)
	defer stop()
	require.Eventually(t, func() bool {
		calls, _, _ := n.app.counts()
		return calls == 2
	}, 5*time.Second, 10*time.Millisecond, "the consensus loop processes the block again")

	peer := types.NodeID("aa00000000000000000000000000000000000000")
	n.cs.msgInfoQueue.admitPeer(peer)
	for _, v := range precommits {
		require.NoError(t, n.cs.msgInfoQueue.send(n.ctx, &VoteMessage{Vote: v}, peer))
	}
	require.Eventually(t, func() bool {
		return n.cs.GetStateData().TriggeredTimeoutPrecommit
	}, 5*time.Second, 10*time.Millisecond, "precommit wait starts once every precommit counts")
	stop()

	sd := n.cs.GetStateData()
	calls, accepted, rejected := n.app.counts()
	require.Equal(t, [3]int{2, 2, 2}, [3]int{calls, accepted, rejected})
	require.Nil(t, sd.Proposal)
	require.True(t, processedFor(&sd.RoundState, 1))
	require.Equal(t, int32(1), sd.Round)
}

// firePolkaRetry waits until the pending polka block retry is due and runs it,
// as the consensus loop does.
func (n *round1Node) firePolkaRetry(t *testing.T) {
	t.Helper()
	due := n.cs.blockExecutor.polkaRetryC()
	require.NotNil(t, due, "a polka block retry is pending")
	select {
	case <-due:
	case <-time.After(2 * polkaRetryMaxDelay):
		require.FailNow(t, "the polka block retry never became due")
	}
	n.cs.blockExecutor.retryPolkaBlock(n.ctx, &n.sd.RoundState)
}

// While a retry is pending, further triggers for the same block do not process
// it: only the retry does, with a growing delay after every failure.
func TestStatePolkaBlockRetryCoalescesDuplicateTriggers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n, _ := newLaggingPolkaNode(ctx, t, &verifyExtensionContractApp{transientFailures: 2})
	n.deliverParts(t)
	for range 3 {
		require.NoError(t, n.cs.ctrl.Dispatch(n.ctx, &ProposalCompletedEvent{Height: 1}, &n.sd))
	}
	calls, _, _ := n.app.counts()
	require.Equal(t, 1, calls, "a pending retry holds off further attempts")

	n.firePolkaRetry(t)
	calls, _, _ = n.app.counts()
	require.Equal(t, 2, calls)
	require.Equal(t, 2*polkaRetryMinDelay, n.cs.blockExecutor.polkaRetry.delay, "the delay doubles")
	require.NoError(t, n.cs.ctrl.Dispatch(n.ctx, &ProposalCompletedEvent{Height: 1}, &n.sd))
	calls, _, _ = n.app.counts()
	require.Equal(t, 2, calls, "a pending retry holds off further attempts")

	n.firePolkaRetry(t)
	calls, _, _ = n.app.counts()
	require.Equal(t, 3, calls, "two failed attempts and one success")
	require.True(t, processedFor(&n.sd.RoundState, 1))
	require.Nil(t, n.cs.blockExecutor.polkaRetryC(), "no retry after a success")
}

// A retry that becomes due after the node no longer awaits the block it was
// scheduled for is dropped without processing anything.
func TestStatePolkaBlockRetryIgnoresStaleTarget(t *testing.T) {
	testCases := map[string]func(t *testing.T, n *round1Node){
		// Even the same block, with a polka in the next round, is not processed
		// by a retry for the previous round.
		"next round": func(t *testing.T, n *round1Node) {
			n.sd.updateRoundStep(2, cstypes.RoundStepPrecommit)
			for _, vs := range n.peers {
				vs.Round = 2
				added, err := n.sd.Votes.AddVote(n.vote(t, vs, tmproto.PrevoteType, n.blockID))
				require.NoError(t, err)
				require.True(t, added)
			}
			polka, ok := n.sd.Votes.Prevotes(2).TwoThirdsMajority()
			require.True(t, ok && polka.Equals(n.blockID))
		},
		// A commit was applied and the node moved to the next height.
		"next height": func(_ *testing.T, n *round1Node) {
			n.sd.Height++
		},
		"block no longer complete": func(_ *testing.T, n *round1Node) {
			n.sd.ProposalBlock = nil
			n.sd.ProposalBlockParts = types.NewPartSetFromHeader(n.blockID.PartSetHeader)
		},
		"block processed elsewhere": func(t *testing.T, n *round1Node) {
			require.NoError(t, n.cs.blockExecutor.ensureProcess(n.ctx, &n.sd.RoundState, 1))
		},
	}
	for name, change := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			n, _ := newLaggingPolkaNode(ctx, t, &verifyExtensionContractApp{transientFailures: 1})
			n.deliverParts(t)
			change(t, n)
			before, _, _ := n.app.counts()

			n.firePolkaRetry(t)
			calls, _, _ := n.app.counts()
			require.Equal(t, before, calls, "a stale retry processes nothing")
			require.Nil(t, n.cs.blockExecutor.polkaRetryC(), "a stale retry is dropped")
		})
	}
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

// A block that completes in the precommit step before its polka is processed
// once the polka forms, so that the precommits for it verify.
func TestStatePolkaFormedAfterTheBlockCompletedInPrecommitIsProcessed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := newRound1Node(ctx, t, &verifyExtensionContractApp{}, cstypes.RoundStepPrevote)
	n.deliverProposal(t)
	// +2/3 prevotes of any kind, no polka: the node times out and precommits nil.
	n.deliverVotes(t, tmproto.PrevoteType, types.BlockID{}, n.self)
	n.deliverVotes(t, tmproto.PrevoteType, n.blockID, n.peers[:2]...)
	require.Equal(t, cstypes.RoundStepPrevoteWait, n.sd.Step)
	n.timeoutPrevote(t)

	n.deliverParts(t)
	require.True(t, n.sd.ProposalBlock.HashesTo(n.blockID.Hash))
	calls, _, _ := n.app.counts()
	require.Equal(t, 0, calls, "a block without its polka is not processed")

	// The delayed prevote forms the polka.
	n.deliverVotes(t, tmproto.PrevoteType, n.blockID, n.peers[2])
	calls, _, _ = n.app.counts()
	require.Equal(t, 1, calls, "the polka block is processed once its polka forms")
	require.True(t, processedFor(&n.sd.RoundState, 1))
	require.Equal(t, cstypes.RoundStepPrecommit, n.sd.Step, "no vote is cast again")

	n.deliverVotes(t, tmproto.PrecommitType, types.BlockID{}, n.self)
	n.deliverVotes(t, tmproto.PrecommitType, n.blockID, n.peers[:2]...)
	calls, accepted, rejected := n.app.counts()
	require.Equal(t, [3]int{1, 2, 0}, [3]int{calls, accepted, rejected})
	require.True(t, n.sd.TriggeredTimeoutPrecommit, "precommit wait starts once every precommit counts")
}

// A block that completes before its polka is processed once the polka forms in
// the precommit wait, so that the precommits for it verify.
func TestStatePolkaFormedAfterTheBlockCompletedInPrecommitWaitIsProcessed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := newRound1Node(ctx, t, &verifyExtensionContractApp{}, cstypes.RoundStepPrevote)
	n.deliverProposal(t)
	n.deliverVotes(t, tmproto.PrevoteType, types.BlockID{}, n.self)
	n.deliverVotes(t, tmproto.PrevoteType, n.blockID, n.peers[:2]...)
	n.timeoutPrevote(t)
	n.deliverParts(t)
	// +2/3 precommits for nil start the precommit wait.
	n.deliverVotes(t, tmproto.PrecommitType, types.BlockID{}, n.self, n.peers[1], n.peers[2])
	require.True(t, n.sd.TriggeredTimeoutPrecommit)
	calls, _, _ := n.app.counts()
	require.Equal(t, 0, calls, "a block without its polka is not processed")

	// The delayed prevote forms the polka.
	n.deliverVotes(t, tmproto.PrevoteType, n.blockID, n.peers[2])
	calls, _, _ = n.app.counts()
	require.Equal(t, 1, calls, "the polka block is processed once its polka forms")
	require.True(t, processedFor(&n.sd.RoundState, 1))
	require.Equal(t, cstypes.RoundStepPrecommit, n.sd.Step, "no vote is cast again")

	n.deliverVotes(t, tmproto.PrecommitType, n.blockID, n.peers[0])
	_, accepted, rejected := n.app.counts()
	require.Equal(t, [2]int{1, 0}, [2]int{accepted, rejected})
}

// A polka block that the node precommits nil because its proposal's timestamp
// differs from the block's is processed, so that the precommits for it verify.
func TestStatePolkaBlockPrecommittedNilOnATimestampMismatchIsProcessed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := newRound1Node(ctx, t, &verifyExtensionContractApp{}, cstypes.RoundStepPrevote)
	n.deliverProposalAt(t, n.block.Time.Add(time.Millisecond))
	n.deliverParts(t)
	require.True(t, n.sd.ProposalBlock.HashesTo(n.blockID.Hash))

	n.deliverVotes(t, tmproto.PrevoteType, types.BlockID{}, n.self)
	n.deliverVotes(t, tmproto.PrevoteType, n.blockID, n.peers...)
	require.Equal(t, cstypes.RoundStepPrecommit, n.sd.Step)
	require.Equal(t, int32(-1), n.sd.LockedRound, "nil precommitted on the timestamp mismatch")
	calls, _, _ := n.app.counts()
	require.Equal(t, 1, calls, "the polka block is processed once")
	require.True(t, processedFor(&n.sd.RoundState, 1))

	n.deliverVotes(t, tmproto.PrecommitType, types.BlockID{}, n.self)
	n.deliverVotes(t, tmproto.PrecommitType, n.blockID, n.peers[:2]...)
	calls, accepted, rejected := n.app.counts()
	require.Equal(t, [3]int{1, 2, 0}, [3]int{calls, accepted, rejected})
	require.True(t, n.sd.TriggeredTimeoutPrecommit, "precommit wait starts once every precommit counts")
}

func TestStatePolkaBlockWithDroppedProposalIsProcessedOnPrecommit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := newRound1Node(ctx, t, &verifyExtensionContractApp{}, cstypes.RoundStepPropose)

	// The proposer signs a different hash but the held block's part set.
	n.blockID = forgeField(t, n.blockID, "hash")
	n.deliverProposal(t)
	n.blockID = n.block.BlockID(n.parts)
	n.deliverParts(t)
	require.Nil(t, n.sd.Proposal, "the completed block invalidates the signed proposal")
	require.True(t, n.sd.ProposalBlock.BlockID(n.sd.ProposalBlockParts).Equals(n.blockID))
	require.Equal(t, cstypes.RoundStepPropose, n.sd.Step)

	// timeoutPropose prevotes nil; the peers' polka arrives before timeoutPrevote.
	require.NoError(t, n.cs.ctrl.Dispatch(n.ctx, &EnterPrevoteEvent{Height: 1, Round: 1}, &n.sd))
	n.deliverVotes(t, tmproto.PrevoteType, types.BlockID{}, n.self)
	n.deliverVotes(t, tmproto.PrevoteType, n.blockID, n.peers...)
	require.Equal(t, cstypes.RoundStepPrevoteWait, n.sd.Step)
	calls, _, _ := n.app.counts()
	require.Zero(t, calls, "the block has not been processed before precommit")

	n.timeoutPrevote(t)
	require.Equal(t, int32(-1), n.sd.LockedRound, "nil precommitted without a proposal")
	n.deliverVotes(t, tmproto.PrecommitType, types.BlockID{}, n.self)
	n.deliverVotes(t, tmproto.PrecommitType, n.blockID, n.peers[:2]...)
	calls, accepted, rejected := n.app.counts()
	require.Equal(t, [3]int{1, 2, 0}, [3]int{calls, accepted, rejected},
		"the held polka block must be processed so peer precommits verify")
	require.True(t, processedFor(&n.sd.RoundState, 1))
	require.True(t, n.sd.TriggeredTimeoutPrecommit, "accepted precommits start the precommit wait")
}

// A polka block the application rejects is processed once: the precommits for
// it stay rejected and do not have it processed anew.
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
	calls, accepted, rejected := n.app.counts()
	require.Equal(t, [3]int{1, 0, 4}, [3]int{calls, accepted, rejected})
	require.Nil(t, n.cs.blockExecutor.polkaRetryC(), "a rejected block is not retried")
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

// A block that completes while this round's polka is for another block is not
// processed. A polka retargets the part set at its block, so the other block's
// parts stop at the part set; only a polka that leaves it be, after a block of
// this round was already valid, lets both exist, so the completion is driven at
// the state machine with the votes placed in the vote set directly.
func TestStateCompletedBlockOtherThanThePolkaBlockIsNotProcessed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := newRound1Node(ctx, t, &verifyExtensionContractApp{}, cstypes.RoundStepPrevote)
	n.deliverProposal(t)
	n.deliverParts(t)
	require.True(t, n.sd.ProposalBlock.HashesTo(n.blockID.Hash))
	n.sd.updateRoundStep(1, cstypes.RoundStepPrecommit)

	other := factory.MakeBlockID()
	for _, vs := range n.peers {
		added, err := n.sd.Votes.AddVote(n.vote(t, vs, tmproto.PrevoteType, other))
		require.NoError(t, err)
		require.True(t, added)
	}
	polka, ok := n.sd.Votes.Prevotes(1).TwoThirdsMajority()
	require.True(t, ok)
	require.True(t, polka.Equals(other))

	require.NoError(t, n.cs.ctrl.Dispatch(n.ctx, &ProposalCompletedEvent{Height: 1}, &n.sd))
	calls, _, _ := n.app.counts()
	require.Equal(t, 0, calls)
	require.False(t, processedFor(&n.sd.RoundState, 1))
	require.Equal(t, int32(-1), n.sd.ValidRound)
}
