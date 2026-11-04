package consensus

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/abci/example/kvstore"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// processedProposal identifies the block an application last ran
// ProcessProposal for.
type processedProposal struct {
	height int64
	round  int32
	hash   []byte
}

// extendVoteContractApp wraps an application with the ExtendVote contract Dash
// Drive enforces: ExtendVote is only answered for the exact (height, round,
// block hash) of the last ProcessProposal call, and fails otherwise -- which
// Tenderdash turns into a panic. Every call is recorded so a test can assert on
// the order, and every contract violation is recorded so a test fails on the
// cause rather than only on the stalled state machine that follows the panic.
type extendVoteContractApp struct {
	abci.Application

	mtx        sync.Mutex
	last       *processedProposal
	extended   []processedProposal
	violations []string
}

func newExtendVoteContractApp(t *testing.T, opts ...kvstore.OptFunc) *extendVoteContractApp {
	t.Helper()
	app, err := kvstore.NewMemoryApp(opts...)
	require.NoError(t, err)
	return &extendVoteContractApp{Application: app}
}

func (app *extendVoteContractApp) PrepareProposal(
	ctx context.Context,
	req *abci.RequestPrepareProposal,
) (*abci.ResponsePrepareProposal, error) {
	app.mtx.Lock()
	// Drive starts a new block execution context here; the block hash is not
	// known until ProcessProposal.
	app.last = nil
	app.mtx.Unlock()
	return app.Application.PrepareProposal(ctx, req)
}

func (app *extendVoteContractApp) ProcessProposal(
	ctx context.Context,
	req *abci.RequestProcessProposal,
) (*abci.ResponseProcessProposal, error) {
	resp, err := app.Application.ProcessProposal(ctx, req)
	if err == nil && resp.IsAccepted() {
		app.mtx.Lock()
		app.last = &processedProposal{height: req.Height, round: req.Round, hash: bytes.Clone(req.Hash)}
		app.mtx.Unlock()
	}
	return resp, err
}

func (app *extendVoteContractApp) ExtendVote(
	ctx context.Context,
	req *abci.RequestExtendVote,
) (*abci.ResponseExtendVote, error) {
	app.mtx.Lock()
	last := app.last
	app.extended = append(app.extended, processedProposal{height: req.Height, round: req.Round, hash: bytes.Clone(req.Hash)})
	if last == nil || last.height != req.Height || last.round != req.Round || !bytes.Equal(last.hash, req.Hash) {
		violation := fmt.Sprintf("ExtendVote for height %d round %d block %X without a preceding ProcessProposal; last processed: %+v",
			req.Height, req.Round, req.Hash, last)
		app.violations = append(app.violations, violation)
		app.mtx.Unlock()
		return nil, fmt.Errorf("received extend votes request for wrong block: %s", violation)
	}
	app.mtx.Unlock()
	return app.Application.ExtendVote(ctx, req)
}

func (app *extendVoteContractApp) Violations() []string {
	app.mtx.Lock()
	defer app.mtx.Unlock()
	return append([]string(nil), app.violations...)
}

func (app *extendVoteContractApp) Extended() []processedProposal {
	app.mtx.Lock()
	defer app.mtx.Unlock()
	return append([]processedProposal(nil), app.extended...)
}

// The later-round polka arrives first and skips the validator into that round.
// Completing the proposal must relock only when its entire BlockID matches.
func TestStateLock_RelockPolkaBeforeProposal(t *testing.T) {
	for _, proposalKind := range []string{"different block", "same hash different BlockID", "same block"} {
		t.Run(proposalKind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			logger := log.NewNopLogger()
			config := configSetup(t)
			consensusParams := factory.ConsensusParams()
			consensusParams.Timeout.Propose = 10 * time.Second

			app := newExtendVoteContractApp(t)
			cs1, vss := makeState(ctx, t, makeStateArgs{config: config, consensusParams: consensusParams, logger: logger, application: app})
			vs2, vs3, vs4 := vss[1], vss[2], vss[3]
			stateData := cs1.GetStateData()
			height, round := stateData.Height, stateData.Round

			proposalCh := subscribe(ctx, t, cs1.eventBus, types.EventQueryCompleteProposal)
			proTxHash, err := cs1.privValidator.GetProTxHash(ctx)
			require.NoError(t, err)
			voteCh := subscribeToVoter(ctx, t, cs1, proTxHash)
			lockCh := subscribe(ctx, t, cs1.eventBus, types.EventQueryLock)
			relockCh := subscribe(ctx, t, cs1.eventBus, types.EventQueryRelock)
			newRoundCh := subscribe(ctx, t, cs1.eventBus, types.EventQueryNewRound)

			// Round 0: cs1 locks on its proposal B and precommits it.
			startTestRound(ctx, cs1, height, round)
			ensureNewRound(t, newRoundCh, height, round)
			ensureNewProposal(t, proposalCh, height, round)
			rs := cs1.GetRoundState()
			blockID := rs.BlockID()
			ensurePrevote(t, voteCh, height, round)
			signAddVotes(ctx, t, cs1, tmproto.PrevoteType, config.ChainID(), blockID, vs2, vs3, vs4)
			ensureLock(t, lockCh, height, round)
			ensurePrecommit(t, voteCh, height, round)
			validatePrecommit(ctx, t, cs1, round, round, vss[0], blockID.Hash, blockID.Hash)

			// Round 1: the other validators have moved on and prevoted B; their prevotes
			// reach cs1 while it is still in round 0 and skip it into round 1.
			incrementRound(vs2, vs3, vs4)
			round++
			signAddVotes(ctx, t, cs1, tmproto.PrevoteType, config.ChainID(), blockID, vs2, vs3, vs4)
			ensureNewRound(t, newRoundCh, height, round)

			var propR1 *types.Proposal
			var propBlockR1 *types.Block
			if proposalKind == "different block" {
				cs2 := newState(ctx, t, logger, stateData.state, vs2, newKVStoreFunc(t)(logger, ""))
				propR1, propBlockR1 = decideProposal(ctx, t, cs2, vs2, vs2.Height, vs2.Round)
				require.NotEqual(t, propBlockR1.Hash(), blockID.Hash)
			} else {
				pb, err := rs.ProposalBlock.ToProto()
				require.NoError(t, err)
				propBlockR1, err = types.BlockFromProto(pb)
				require.NoError(t, err)
				if proposalKind == "same hash different BlockID" {
					propBlockR1.CoreChainLockedHeight++
				}
				proposalID := propBlockR1.BlockID(nil)
				require.Equal(t, blockID.Hash, proposalID.Hash)
				require.Equal(t, proposalKind == "same block", proposalID.Equals(blockID))
				propR1 = types.NewProposal(height, 1, round, 0, proposalID, propBlockR1.Time)
				p := propR1.ToProto()
				_, valSet := cs1.GetValidatorSet()
				_, err = vs2.SignProposal(ctx, config.ChainID(), valSet.QuorumType, valSet.QuorumHash, p)
				require.NoError(t, err)
				propR1.Signature = p.Signature
			}
			propBlockR1Parts, err := propBlockR1.MakePartSet(types.BlockPartSizeBytes)
			require.NoError(t, err)
			err = cs1.SetProposalAndBlock(ctx, propR1, propBlockR1Parts, "some peer")
			require.NoError(t, err)
			ensureNewProposal(t, proposalCh, height, round)

			ensurePrevote(t, voteCh, height, round)
			if proposalKind == "same block" {
				validatePrevote(ctx, t, cs1, round, vss[0], blockID.Hash)
				ensureRelock(t, relockCh, height, round)
				ensurePrecommit(t, voteCh, height, round)
				validatePrecommit(ctx, t, cs1, round, round, vss[0], blockID.Hash, blockID.Hash)
				rs = cs1.GetRoundState()
				require.Equal(t, round, rs.ValidRound)
				require.True(t, rs.ValidBlock.BlockID(rs.ValidBlockParts).Equals(blockID))
				extended := app.Extended()
				require.Len(t, extended, 2)
				require.Equal(t, round, extended[1].round)
				require.Equal(t, []byte(blockID.Hash), extended[1].hash)
			} else {
				validatePrevote(ctx, t, cs1, round, vss[0], nil)
				ensurePrecommit(t, voteCh, height, round)
				validatePrecommit(ctx, t, cs1, round, 0, vss[0], nil, blockID.Hash)
				ensureNoNewEventOnChannel(t, relockCh)
				require.Len(t, app.Extended(), 1)
			}
			require.Empty(t, app.Violations())

		})
	}
}

// TestStateLock_RelockLateProposalProcessesBeforeExtending covers a validator
// locked on block B that receives the proposal for B in a later round only after
// its propose timeout, so it prevoted nil without processing the proposal for
// that round. When +2/3 prevotes for B arrive it relocks and precommits B, and
// ProcessProposal for B in this round must run before ExtendVote does.
func TestStateLock_RelockLateProposalProcessesBeforeExtending(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	config := configSetup(t)

	app := newExtendVoteContractApp(t)
	cs1, vss := makeState(ctx, t, makeStateArgs{config: config, application: app})
	vs2, vs3, vs4 := vss[1], vss[2], vss[3]
	stateData := cs1.GetStateData()
	height, round := stateData.Height, stateData.Round

	timeoutWaitCh := subscribe(ctx, t, cs1.eventBus, types.EventQueryTimeoutWait)
	proposalCh := subscribe(ctx, t, cs1.eventBus, types.EventQueryCompleteProposal)
	proTxHash, err := cs1.privValidator.GetProTxHash(ctx)
	require.NoError(t, err)
	voteCh := subscribeToVoter(ctx, t, cs1, proTxHash)
	lockCh := subscribe(ctx, t, cs1.eventBus, types.EventQueryLock)
	relockCh := subscribe(ctx, t, cs1.eventBus, types.EventQueryRelock)
	newRoundCh := subscribe(ctx, t, cs1.eventBus, types.EventQueryNewRound)

	// Round 0: lock on B, everyone else precommits nil.
	startTestRound(ctx, cs1, height, round)
	ensureNewRound(t, newRoundCh, height, round)
	ensureNewProposal(t, proposalCh, height, round)
	stateData = cs1.GetStateData()
	blockID := stateData.BlockID()
	theBlock := stateData.ProposalBlock
	theBlockParts := stateData.ProposalBlockParts
	ensurePrevote(t, voteCh, height, round)
	signAddVotes(ctx, t, cs1, tmproto.PrevoteType, config.ChainID(), blockID, vs2, vs3, vs4)
	ensureLock(t, lockCh, height, round)
	ensurePrecommit(t, voteCh, height, round)
	validatePrecommit(ctx, t, cs1, round, round, vss[0], blockID.Hash, blockID.Hash)
	timeoutProposeCh := subscribe(ctx, t, cs1.eventBus, types.EventQueryTimeoutPropose)
	signAddVotes(ctx, t, cs1, tmproto.PrecommitType, config.ChainID(), types.BlockID{}, vs2, vs3, vs4)
	ensureNewTimeout(t, timeoutWaitCh, height, round, stateData.voteTimeout(round).Nanoseconds())

	// Round 1: the proposal for B misses the propose timeout, so cs1 prevotes
	// nil without processing it.
	incrementRound(vs2, vs3, vs4)
	round++
	ensureNewRound(t, newRoundCh, height, round)
	ensureNewTimeout(t, timeoutProposeCh, height, round, stateData.proposeTimeout(round).Nanoseconds())
	ensurePrevote(t, voteCh, height, round)
	validatePrevote(ctx, t, cs1, round, vss[0], nil)

	propR1 := types.NewProposal(height, 1, round, stateData.ValidRound, blockID, theBlock.Time)
	p := propR1.ToProto()
	_, valSet := cs1.GetValidatorSet()
	_, err = vs2.SignProposal(ctx, stateData.state.ChainID, valSet.QuorumType, valSet.QuorumHash, p)
	require.NoError(t, err)
	propR1.Signature = p.Signature
	err = cs1.SetProposalAndBlock(ctx, propR1, theBlockParts, "some peer")
	require.NoError(t, err)
	ensureNewProposal(t, proposalCh, height, round)

	signAddVotes(ctx, t, cs1, tmproto.PrevoteType, config.ChainID(), blockID, vs2, vs3, vs4)

	ensureRelock(t, relockCh, height, round)
	ensurePrecommit(t, voteCh, height, round)
	require.Empty(t, app.Violations())
	validatePrecommit(ctx, t, cs1, round, round, vss[0], blockID.Hash, blockID.Hash)
	// The polka handling made B valid for this round; relock relies on it.
	rs := cs1.GetRoundState()
	require.Equal(t, round, rs.ValidRound)
	require.True(t, rs.ValidBlock.BlockID(rs.ValidBlockParts).Equals(blockID))

	extended := app.Extended()
	require.NotEmpty(t, extended)
	last := extended[len(extended)-1]
	require.Equal(t, round, last.round)
	require.Equal(t, []byte(blockID.Hash), last.hash)
}

// An equivocating proposer supplies A, then the polka retargets the round to B.
// Equal application outputs do not make A the execution context for B.
func TestStateLock_ReplacementProcessesExactBlock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Model Drive with https://github.com/dashpay/platform/pull/4462: execute a replacement
	// at the same height/round after discarding stale context. Drive v4.1.1 rejects it.
	app := newExtendVoteContractApp(t, kvstore.WithDuplicateRequestDetection(false))
	cs, vss := makeState(ctx, t, makeStateArgs{application: app, logger: log.NewNopLogger()})
	ctx = dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash)
	sd := cs.GetStateData()
	_, block := decideProposal(ctx, t, cs, vss[0], sd.Height, 0)
	parts, err := block.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)
	blockID := block.BlockID(parts)
	sd.ProposalBlock, sd.ProposalBlockParts = block, parts
	require.NoError(t, cs.blockExecutor.ensureProcess(ctx, &sd.RoundState, 0))
	sd.LockedBlock, sd.LockedBlockParts, sd.LockedRound = block, parts, 0
	sd.ValidBlock, sd.ValidBlockParts, sd.ValidRound = block, parts, 0
	for _, vs := range vss[1:] {
		vote := signVote(ctx, t, vs, tmproto.PrevoteType, sd.state.ChainID, blockID, sd.Validators.QuorumType, sd.Validators.QuorumHash)
		_, err := sd.Votes.AddVote(vote)
		require.NoError(t, err)
	}
	sd.Votes.SetRound(1)
	sd.updateRoundStep(1, cstypes.RoundStepPropose)
	incrementRound(vss...)

	pb, err := block.ToProto()
	require.NoError(t, err)
	pb.Header.ProposerProTxHash, err = vss[1].GetProTxHash(ctx)
	require.NoError(t, err)
	other, err := types.BlockFromProto(pb)
	require.NoError(t, err)
	require.NotEqual(t, block.Hash(), other.Hash())
	require.Equal(t, block.AppHash, other.AppHash)
	require.Equal(t, block.ResultsHash, other.ResultsHash)
	otherParts, err := other.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)
	sd.Proposal = makeProposal(sd.Height, sd.Round, -1, other, otherParts)
	sd.ProposalReceiveTime = other.Time
	sd.ProposalBlock, sd.ProposalBlockParts = other, otherParts
	require.NoError(t, cs.ctrl.Dispatch(ctx, &EnterPrevoteEvent{Height: sd.Height, Round: sd.Round}, &sd))
	require.True(t, processedFor(&sd.RoundState, sd.Round))

	for _, vs := range vss[1:] {
		vote := signVote(ctx, t, vs, tmproto.PrevoteType, sd.state.ChainID, blockID, sd.Validators.QuorumType, sd.Validators.QuorumHash)
		require.NoError(t, cs.msgDispatcher.dispatch(ctx, &sd, msgInfo{Msg: &VoteMessage{Vote: vote}, PeerID: "peer"}))
	}
	require.Nil(t, sd.Proposal)
	require.Nil(t, sd.ProposalBlock)
	proposal := makeProposal(sd.Height, sd.Round, 0, block, parts)
	protoProposal := proposal.ToProto()
	_, err = vss[1].SignProposal(ctx, sd.state.ChainID, sd.Validators.QuorumType, sd.Validators.QuorumHash, protoProposal)
	require.NoError(t, err)
	proposal.Signature = protoProposal.Signature
	require.NoError(t, cs.msgDispatcher.dispatch(ctx, &sd, msgInfo{Msg: &ProposalMessage{Proposal: proposal}, PeerID: "peer", ReceiveTime: block.Time}))
	for i := uint32(0); i < parts.Total(); i++ {
		require.NoError(t, cs.msgDispatcher.dispatch(ctx, &sd, msgInfo{Msg: &BlockPartMessage{Height: sd.Height, Round: sd.Round, Part: parts.GetPart(int(i))}, PeerID: "peer"}))
	}
	require.True(t, sd.ProposalBlock.BlockID(sd.ProposalBlockParts).Equals(blockID))
	require.NotPanics(t, func() {
		require.NoError(t, cs.ctrl.Dispatch(ctx, &EnterPrecommitEvent{Height: sd.Height, Round: sd.Round}, &sd))
	})
	require.Empty(t, app.Violations())
	require.Len(t, app.Extended(), 1)
	require.Equal(t, sd.Round, app.Extended()[0].round)
	require.Equal(t, []byte(blockID.Hash), app.Extended()[0].hash)
	require.Equal(t, sd.Round, sd.LockedRound)
	require.True(t, sd.LockedBlock.BlockID(sd.LockedBlockParts).Equals(blockID))
}

func TestStateLock_SameHashDifferentPartsTakesNewLock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := newExtendVoteContractApp(t)
	cs, vss := makeState(ctx, t, makeStateArgs{application: app, logger: log.NewNopLogger()})
	ctx = dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash)
	sd := cs.GetStateData()
	_, block := decideProposal(ctx, t, cs, vss[0], sd.Height, 0)
	oldParts, err := block.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)
	parts, err := block.MakePartSet(multiPartBlockPartSize)
	require.NoError(t, err)
	blockID := block.BlockID(parts)
	require.Equal(t, block.Hash(), blockID.Hash)
	require.False(t, oldParts.HasHeader(blockID.PartSetHeader))
	sd.LockedBlock, sd.LockedBlockParts, sd.LockedRound = block, oldParts, 0
	sd.ValidBlock, sd.ValidBlockParts, sd.ValidRound = block, oldParts, 0
	sd.ProposalBlock, sd.ProposalBlockParts = block, parts
	sd.Proposal = makeProposal(sd.Height, 1, -1, block, parts)
	sd.ProposalReceiveTime = block.Time
	sd.Votes.SetRound(1)
	sd.updateRoundStep(1, cstypes.RoundStepPrevote)
	incrementRound(vss...)
	for _, vs := range vss[1:] {
		vote := signVote(ctx, t, vs, tmproto.PrevoteType, sd.state.ChainID, blockID, sd.Validators.QuorumType, sd.Validators.QuorumHash)
		require.NoError(t, cs.msgDispatcher.dispatch(ctx, &sd, msgInfo{Msg: &VoteMessage{Vote: vote}, PeerID: "peer"}))
	}
	majority, ok := sd.Votes.Prevotes(sd.Round).TwoThirdsMajority()
	require.True(t, ok)
	require.True(t, majority.Equals(blockID))
	require.NoError(t, cs.ctrl.Dispatch(ctx, &EnterPrecommitEvent{Height: sd.Height, Round: sd.Round}, &sd))
	require.Equal(t, sd.Round, sd.LockedRound)
	require.True(t, sd.LockedBlock.BlockID(sd.LockedBlockParts).Equals(blockID), "the lock must name the complete precommitted value")
	require.True(t, sd.ValidBlock.BlockID(sd.ValidBlockParts).Equals(blockID), "the valid value must name the complete polka")
	require.Empty(t, app.Violations())
}
