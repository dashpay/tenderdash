package consensus

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	tmpubsub "github.com/dashpay/tenderdash/internal/pubsub"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// signRoundSkipVote returns vs's vote to skip round at height, as a peer would
// send it.
func signRoundSkipVote(
	ctx context.Context,
	t *testing.T,
	cs *State,
	vs *validatorStub,
	height int64,
	round int32,
) *types.RoundSkipVote {
	t.Helper()
	stateData := cs.GetStateData()
	proTxHash, err := vs.GetProTxHash(ctx)
	require.NoError(t, err)
	pb := &tmproto.RoundSkipVote{
		Height:             height,
		Round:              round,
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     vs.Index,
	}
	require.NoError(t, vs.SignRoundSkipVote(ctx, stateData.state.ChainID,
		stateData.Validators.QuorumType, stateData.Validators.QuorumHash, pb))
	vote, err := types.RoundSkipVoteFromProto(pb)
	require.NoError(t, err)
	return vote
}

// deliverRoundSkipVote does with a peer's skip vote what the reactor does: it
// queues the vote for the consensus goroutine, which counts it and applies the
// certificate the vote completes.
func deliverRoundSkipVote(ctx context.Context, t *testing.T, cs *State, vote *types.RoundSkipVote) {
	t.Helper()
	wanted, err := cs.roundSkips.wanted(vote)
	require.NoError(t, err)
	require.True(t, wanted)
	require.NoError(t, cs.sendMessage(ctx, &RoundSkipVoteMessage{Vote: vote}, "peer"))
}

// roundSkipTest is a four-validator CONSENSUS_VERSION_2 consensus state whose
// first validator is this node; the other three are stubs that send nothing
// unless the test does.
type roundSkipTest struct {
	cs           *State
	vss          []*validatorStub
	chainID      string
	newRoundCh   <-chan tmpubsub.Message
	proposalCh   <-chan tmpubsub.Message
	ownSkipVotes chan *types.RoundSkipVote
	heartbeats   chan *types.ProposerHeartbeat
	silences     *syncCounter
}

// newRoundSkipTest builds the state and commits height 1, which this node
// proposes, so the test starts with the node waiting at height 2, where the
// proposers of rounds 0, 1 and 2 are stubs 1, 2 and 3 and round 3 is the
// node's. wrap, if set, wraps the node's signer.
func newRoundSkipTest(
	ctx context.Context,
	t *testing.T,
	name string,
	wrap func(types.PrivValidator) types.PrivValidator,
) *roundSkipTest {
	t.Helper()
	baseConfig := configSetup(t)
	config, err := ResetConfig(t, name)
	require.NoError(t, err)
	config.Consensus.CreateEmptyBlocks = false

	params := factory.ConsensusParams(func(c *types.ConsensusParams) {
		c.Version.ConsensusVersion = int32(tmproto.VersionParams_CONSENSUS_VERSION_2)
		c.Timeout.Vote = 50 * time.Millisecond
		c.Timeout.VoteDelta = 5 * time.Millisecond
		c.Timeout.ProposerHeartbeat = 50 * time.Millisecond
		c.Timeout.ProposerSilence = 300 * time.Millisecond
	})
	state, privVals := makeGenesisState(ctx, t, baseConfig, genesisStateArgs{
		Validators: 4,
		Params:     params,
	})
	pv := privVals[0]
	if wrap != nil {
		pv = wrap(pv)
	}
	cs := newStateWithConfig(ctx, t, log.NewNopLogger(), config, state, pv, NewCounterApplication())
	test := &roundSkipTest{
		cs:           cs,
		vss:          make([]*validatorStub, len(privVals)),
		chainID:      config.ChainID(),
		newRoundCh:   subscribe(ctx, t, cs.eventBus, types.EventQueryNewRound),
		proposalCh:   subscribe(ctx, t, cs.eventBus, types.EventQueryCompleteProposal),
		ownSkipVotes: make(chan *types.RoundSkipVote, 10),
		heartbeats:   make(chan *types.ProposerHeartbeat, 1000),
		silences:     &syncCounter{},
	}
	cs.metrics.ProposerSilences = test.silences
	for i, privVal := range privVals {
		test.vss[i] = newValidatorStub(privVal, int32(i), state.InitialHeight)
	}
	cs.emitter.AddListener(roundSkipVoteEventName, func(data eventemitter.EventData) error {
		test.ownSkipVotes <- data.(*types.RoundSkipVote)
		return nil
	})
	cs.emitter.AddListener(proposerHeartbeatEventName, func(data eventemitter.EventData) error {
		test.heartbeats <- data.(*types.ProposerHeartbeat)
		return nil
	})

	startTestRound(ctx, cs, 1, 0)
	ensureNewRound(t, test.newRoundCh, 1, 0)
	ensureNewProposal(t, test.proposalCh, 1, 0)
	rs := cs.GetRoundState()
	signAddVotes(ctx, t, cs, tmproto.PrecommitType, test.chainID, rs.BlockID(), test.vss[1:]...)
	ensureNewRound(t, test.newRoundCh, 2, 0)
	return test
}

// ownSkipVote waits for the skip vote this node casts for round.
func (test *roundSkipTest) ownSkipVote(t *testing.T, round int32) *types.RoundSkipVote {
	t.Helper()
	select {
	case vote := <-test.ownSkipVotes:
		require.Equal(t, int64(2), vote.Height)
		require.Equal(t, round, vote.Round)
		return vote
	case <-time.After(5 * time.Second):
		t.Fatalf("no skip vote for silent round %d", round)
		return nil
	}
}

// completeSkip has the stubs other than silent vote to skip round, which
// completes the certificate together with this node's vote.
func (test *roundSkipTest) completeSkip(ctx context.Context, t *testing.T, round int32, silent int) {
	t.Helper()
	for i := 1; i < len(test.vss); i++ {
		if i != silent {
			deliverRoundSkipVote(ctx, t, test.cs, signRoundSkipVote(ctx, t, test.cs, test.vss[i], 2, round))
		}
	}
	ensureNewRound(t, test.newRoundCh, 2, round+1)
}

func TestStateRoundSkip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	test := newRoundSkipTest(ctx, t, "consensus_round_skip_test", nil)

	// Height 2: the proposers of rounds 0, 1 and 2 are stubs, which send no
	// heartbeats. In each round this node votes to skip, two validators other
	// than the silent proposer join it, and the certificate moves it on.
	for round, silent := range []int{1, 2, 3} {
		round := int32(round)
		test.ownSkipVote(t, round)
		test.completeSkip(ctx, t, round, silent)

		stateData := test.cs.GetStateData()
		require.NotNil(t, stateData.RoundSkip)
		assert.Equal(t, round, stateData.RoundSkip.Round)
	}

	// Round 3 is this node's. It keeps waiting, now as the proposer, and
	// announces itself; no block is made before there is something to propose.
	deadline := time.After(5 * time.Second)
	for heard := false; !heard; {
		select {
		case hb := <-test.heartbeats:
			heard = hb.Height == 2 && hb.Round == 3
		case <-deadline:
			t.Fatal("no heartbeat from the proposer of round 3")
		}
	}
	stateData := test.cs.GetStateData()
	assert.Equal(t, int32(3), stateData.Round)
	assert.Equal(t, cstypes.RoundStepNewRound, stateData.Step)
	ensureNoNewEventOnChannel(t, test.proposalCh)

	checkTxsRange(ctx, t, test.cs, 0, 1)
	ensureNewProposal(t, test.proposalCh, 2, 3)
}

// The mempool reports transactions once per height. A validator that heard the
// report in a round, and is then moved on by that round's certificate, has no
// report left to end a wait in the next round, so the next round must not wait.
func TestStateRoundSkipAfterTxsReported(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	test := newRoundSkipTest(ctx, t, "consensus_round_skip_txs_reported_test", nil)

	// Round 0: its proposer is silent and this node votes to skip it.
	test.ownSkipVote(t, 0)
	// A transaction arrives before the certificate does: the node hears the
	// height's one report and steps into the propose step of round 0.
	checkTxsRange(ctx, t, test.cs, 0, 1)
	require.Eventually(t, func() bool {
		stateData := test.cs.GetStateData()
		return stateData.Round == 0 && stateData.Step >= cstypes.RoundStepPropose
	}, 3*time.Second, 10*time.Millisecond, "the node did not act on the transaction")

	// The certificate moves it to round 1, which must not wait for a report
	// that has already come.
	test.completeSkip(ctx, t, 0, 1)
	require.Eventually(t, func() bool {
		stateData := test.cs.GetStateData()
		return stateData.Round == 1 && stateData.Step >= cstypes.RoundStepPropose
	}, 3*time.Second, 10*time.Millisecond, "round 1 waits for transactions the mempool already reported")
}

// Skip votes do not stop validators from voting, so a late proposal can win a
// polka in the round they skip, before or after the certificate for that round
// reaches a node. A node that has the polka but none of the block's parts has
// no valid block, and the certificate drops the download. The next round must
// not wait for transactions, whether the polka was recorded before it began or
// while it waits: they may exist only in the proposed block, and a waiting
// proposer would keep announcing itself with nothing to propose.
func TestStateRoundSkipAfterAPolkaWithoutItsBlock(t *testing.T) {
	for _, certificateFirst := range []bool{false, true} {
		name := "polka first"
		if certificateFirst {
			name = "certificate first"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			test := newRoundSkipTest(ctx, t, "consensus_round_skip_after_polka_test", nil)

			// Round 0: its proposer is silent and this node votes to skip it.
			test.ownSkipVote(t, 0)
			if certificateFirst {
				test.completeSkip(ctx, t, 0, 1)
				require.Equal(t, cstypes.RoundStepNewRound, test.cs.GetStateData().Step, "round 1 waits")
			}
			// The proposal of round 0 comes late, and the other three validators
			// prevote its block. This node hears their prevotes, but none of the
			// block's parts, and no transaction reaches its mempool.
			blockID := factory.MakeBlockID()
			for _, vs := range test.vss[1:] {
				vs.Height, vs.Round = 2, 0
			}
			signAddVotes(ctx, t, test.cs, tmproto.PrevoteType, test.chainID, blockID, test.vss[1:]...)
			require.Eventually(t, func() bool {
				polRound, polBlockID := test.cs.GetStateData().Votes.POLInfo()
				return polRound == 0 && polBlockID.Equals(blockID)
			}, 3*time.Second, 10*time.Millisecond, "the polka was not recorded")
			require.Equal(t, int32(-1), test.cs.GetStateData().ValidRound,
				"the block is not complete, so it is not valid yet")
			if !certificateFirst {
				test.completeSkip(ctx, t, 0, 1)
			}

			require.Eventually(t, func() bool {
				stateData := test.cs.GetStateData()
				return stateData.Round == 1 && stateData.Step >= cstypes.RoundStepPropose
			}, 3*time.Second, 10*time.Millisecond, "round 1 waits for transactions though round 0 has a polka")
		})
	}
}

// A proposer can send its proposal and withhold the block's parts. The
// proposal stops the silence watch, so a node waiting for transactions takes
// it as the end of the wait and enters the propose step, whose timeout moves
// the round on: with no transaction in its mempool, nothing else would.
func TestStateWaitingRoundTakesAProposalWithoutItsBlock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	test := newRoundSkipTest(ctx, t, "consensus_round_skip_bare_proposal_test", nil)

	// Round 0 is skipped, and round 1, whose proposer is stub 2, waits.
	test.ownSkipVote(t, 0)
	test.completeSkip(ctx, t, 0, 1)
	stateData := test.cs.GetStateData()
	require.Equal(t, cstypes.RoundStepNewRound, stateData.Step)

	// Stub 2 sends a signed proposal for round 1, and none of its block's parts.
	proposal := types.NewProposal(2, stateData.state.LastCoreChainLockedBlockHeight, 1, -1,
		factory.MakeBlockID(), tmtime.Now())
	pb := proposal.ToProto()
	_, err := test.vss[2].SignProposal(ctx, test.chainID,
		stateData.Validators.QuorumType, stateData.Validators.QuorumHash, pb)
	require.NoError(t, err)
	proposal.Signature = pb.Signature
	require.NoError(t, test.cs.sendMessage(ctx, &ProposalMessage{Proposal: proposal}, "peer"))

	// The propose timeout runs out with the block incomplete, and the node
	// prevotes.
	require.Eventually(t, func() bool {
		stateData := test.cs.GetStateData()
		return stateData.Round == 1 && stateData.Proposal != nil && stateData.Step >= cstypes.RoundStepPrevote
	}, 3*time.Second, 10*time.Millisecond, "the proposal ended the silence watch with no timeout running")
}

// A proposer that sends one more heartbeat after this validator voted to skip
// it, and then goes silent again, gets the same vote again: broadcast, not
// signed a second time.
func TestStateRoundSkipVoteNotSignedTwice(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signer := &countingSkipVoteSigner{}
	test := newRoundSkipTest(ctx, t, "consensus_round_skip_vote_once_test", func(pv types.PrivValidator) types.PrivValidator {
		signer.PrivValidator = pv
		return signer
	})

	first := test.ownSkipVote(t, 0)

	// The silent proposer of round 0, stub 1, sends one heartbeat.
	stateData := test.cs.GetStateData()
	proposer := test.vss[1]
	proTxHash, err := proposer.GetProTxHash(ctx)
	require.NoError(t, err)
	pb := &tmproto.ProposerHeartbeat{
		Height:            2,
		Round:             0,
		Sequence:          1,
		ProposerProTxHash: proTxHash,
		Timestamp:         tmtime.Now(),
	}
	require.NoError(t, proposer.SignProposerHeartbeat(ctx, stateData.state.ChainID,
		stateData.Validators.QuorumType, stateData.Validators.QuorumHash, pb))
	hb, err := types.ProposerHeartbeatFromProto(pb)
	require.NoError(t, err)
	relay, err := test.cs.liveness.heard(hb)
	require.NoError(t, err)
	require.True(t, relay)

	again := test.ownSkipVote(t, 0)
	assert.Same(t, first, again, "the vote cast before is broadcast again")
	assert.Equal(t, int32(1), signer.skipVotes.Load(), "the vote is not signed twice")
}

// A broadcast skip vote can be lost on its way to peers, so while the round
// stays silent the vote goes out again every silence timeout: the same vote,
// not signed again.
func TestStateRoundSkipVoteSentAgainWhileSilent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signer := &countingSkipVoteSigner{}
	test := newRoundSkipTest(ctx, t, "consensus_round_skip_vote_resent_test", func(pv types.PrivValidator) types.PrivValidator {
		signer.PrivValidator = pv
		return signer
	})

	first := test.ownSkipVote(t, 0)
	for i := 0; i < 2; i++ {
		again := test.ownSkipVote(t, 0)
		assert.Same(t, first, again, "resend %d", i+1)
	}
	assert.Equal(t, int32(1), signer.skipVotes.Load(), "the vote is not signed again")

	// The certificate moves the node on, and ends the resending with the
	// round.
	test.completeSkip(ctx, t, 0, 1)
	vote := test.ownSkipVote(t, 1)
	assert.Equal(t, int32(1), vote.Round)
}

// A peer's heartbeats and skip votes reach the consensus goroutine through the
// peer scheduler, and each that is news goes back to the reactor to be relayed,
// naming the peer it came from.
func TestStateLivenessMessagesFromPeers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	test := newRoundSkipTest(ctx, t, "consensus_liveness_from_peers_test", nil)
	relays := make(chan livenessRelay, 10)
	test.cs.emitter.AddListener(livenessRelayEventName, func(data eventemitter.EventData) error {
		relays <- data.(livenessRelay)
		return nil
	})
	nextRelay := func() livenessRelay {
		t.Helper()
		select {
		case relay := <-relays:
			return relay
		case <-time.After(5 * time.Second):
			t.Fatal("nothing relayed")
			return livenessRelay{}
		}
	}

	// The proposer of round 0, stub 1, sends a heartbeat.
	stateData := test.cs.GetStateData()
	proTxHash, err := test.vss[1].GetProTxHash(ctx)
	require.NoError(t, err)
	pb := &tmproto.ProposerHeartbeat{
		Height:            2,
		Round:             0,
		Sequence:          1,
		ProposerProTxHash: proTxHash,
		Timestamp:         tmtime.Now(),
	}
	require.NoError(t, test.vss[1].SignProposerHeartbeat(ctx, stateData.state.ChainID,
		stateData.Validators.QuorumType, stateData.Validators.QuorumHash, pb))
	hb, err := types.ProposerHeartbeatFromProto(pb)
	require.NoError(t, err)
	require.NoError(t, test.cs.sendMessage(ctx, &ProposerHeartbeatMessage{Heartbeat: hb}, "peer-a"))

	relay := nextRelay()
	assert.Equal(t, types.NodeID("peer-a"), relay.from)
	require.IsType(t, &ProposerHeartbeatMessage{}, relay.msg)
	assert.Same(t, hb, relay.msg.(*ProposerHeartbeatMessage).Heartbeat)

	// Stub 2 votes to skip round 0.
	vote := signRoundSkipVote(ctx, t, test.cs, test.vss[2], 2, 0)
	require.NoError(t, test.cs.sendMessage(ctx, &RoundSkipVoteMessage{Vote: vote}, "peer-b"))

	relay = nextRelay()
	assert.Equal(t, types.NodeID("peer-b"), relay.from)
	require.IsType(t, &RoundSkipVoteMessage{}, relay.msg)
	assert.Same(t, vote, relay.msg.(*RoundSkipVoteMessage).Vote)
	assert.NotNil(t, test.cs.roundSkips.voteOf(2, 0, vote.ValidatorProTxHash), "the vote was counted")
}

// A skip vote whose signing fails is tried again after another silence
// timeout, rather than never.
func TestStateRoundSkipVoteRetriedAfterSigningFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signer := &countingSkipVoteSigner{}
	signer.failures.Store(1)
	test := newRoundSkipTest(ctx, t, "consensus_round_skip_vote_retry_test", func(pv types.PrivValidator) types.PrivValidator {
		signer.PrivValidator = pv
		return signer
	})

	test.ownSkipVote(t, 0)
	assert.Equal(t, int32(2), signer.skipVotes.Load(), "one failed attempt, then the vote")
	assert.Equal(t, float64(1), test.silences.count(), "the retry is not another silent round")
}

// A signer that does not answer holds up this validator's skip vote, not the
// consensus goroutine: the others' votes still move the node on. It is asked
// for one vote at a time, and its late answer for a round the node left is
// dropped; the next silent round gets its vote once it answers again.
func TestStateRoundSkipVoteSignedOffTheConsensusGoroutine(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signer := &stalledSkipVoteSigner{release: make(chan struct{})}
	test := newRoundSkipTest(ctx, t, "consensus_round_skip_stalled_signer_test", func(pv types.PrivValidator) types.PrivValidator {
		signer.PrivValidator = pv
		return signer
	})

	require.Eventually(t, func() bool { return signer.skipVotes.Load() == 1 }, 5*time.Second, 10*time.Millisecond,
		"the silence of round 0 asks for a vote")
	for _, vs := range test.vss[1:] {
		deliverRoundSkipVote(ctx, t, test.cs, signRoundSkipVote(ctx, t, test.cs, vs, 2, 0))
	}
	ensureNewRound(t, test.newRoundCh, 2, 1)

	// Round 1 goes silent too, while the signer still works on round 0.
	silence := test.cs.GetStateData().state.ConsensusParams.Timeout.TimeoutParamsOrDefaults().ProposerSilenceTimeout(1)
	time.Sleep(3 * silence)
	assert.Equal(t, int32(1), signer.skipVotes.Load(), "one vote at a time")

	// The vote for round 0 comes back after the node left the round, and is
	// not broadcast: the first own vote out is round 1's.
	close(signer.release)
	test.ownSkipVote(t, 1)
	assert.Equal(t, int32(2), signer.skipVotes.Load())
}

// stalledSkipVoteSigner signs skip votes once release is closed, and counts
// them.
type stalledSkipVoteSigner struct {
	types.PrivValidator
	release   chan struct{}
	skipVotes atomic.Int32
}

func (pv *stalledSkipVoteSigner) SignRoundSkipVote(
	ctx context.Context,
	chainID string,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	vote *tmproto.RoundSkipVote,
) error {
	pv.skipVotes.Add(1)
	<-pv.release
	return pv.PrivValidator.SignRoundSkipVote(ctx, chainID, quorumType, quorumHash, vote)
}

// countingSkipVoteSigner counts the skip votes it is asked to sign, and fails
// the first failures of them.
type countingSkipVoteSigner struct {
	types.PrivValidator
	skipVotes atomic.Int32
	failures  atomic.Int32
}

func (pv *countingSkipVoteSigner) SignRoundSkipVote(
	ctx context.Context,
	chainID string,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	vote *tmproto.RoundSkipVote,
) error {
	pv.skipVotes.Add(1)
	if pv.failures.Add(-1) >= 0 {
		return errors.New("signer busy")
	}
	return pv.PrivValidator.SignRoundSkipVote(ctx, chainID, quorumType, quorumHash, vote)
}
