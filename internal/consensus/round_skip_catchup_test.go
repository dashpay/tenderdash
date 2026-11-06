package consensus

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// newRoundSkipCatchupState is a four-validator CONSENSUS_VERSION_2 state that
// is not running and proposes nothing itself, so the test moves it from round
// to round. Its context names the node's validator.
func newRoundSkipCatchupState(ctx context.Context, t *testing.T) (context.Context, *State, []*validatorStub) {
	t.Helper()
	cs, vss := makeState(ctx, t, makeStateArgs{
		validators: 4,
		consensusParams: factory.ConsensusParams(func(c *types.ConsensusParams) {
			c.Version.ConsensusVersion = int32(tmproto.VersionParams_CONSENSUS_VERSION_2)
		}),
	})
	cs.config.DontAutoPropose = true
	return dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash), cs, vss
}

// recoverRoundSkipCertificate returns the certificate that the skip votes of
// the stubs other than the node recover for round at height.
func recoverRoundSkipCertificate(
	ctx context.Context,
	t *testing.T,
	cs *State,
	vss []*validatorStub,
	height int64,
	round int32,
) *types.RoundSkipCertificate {
	t.Helper()
	stateData := cs.GetStateData()
	votes := make([]*types.RoundSkipVote, 0, len(vss)-1)
	for _, vs := range vss[1:] {
		votes = append(votes, signRoundSkipVote(ctx, t, cs, vs, height, round))
	}
	certificate, err := types.RecoverRoundSkipCertificate(stateData.state.ChainID, stateData.Validators, votes)
	require.NoError(t, err)
	return certificate
}

// nilPrevotes returns the nil prevotes for round of the stubs other than the
// node, which are +2/3 of the four validators.
func nilPrevotes(ctx context.Context, t *testing.T, cs *State, vss []*validatorStub, round int32) []*types.Vote {
	t.Helper()
	stateData := cs.GetStateData()
	require.LessOrEqual(t, stateData.Validators.QuorumTypeThresholdCount(), len(vss)-1)
	votes := make([]*types.Vote, 0, len(vss)-1)
	for _, vs := range vss[1:] {
		vs.Height, vs.Round = stateData.Height, round
		votes = append(votes, signVote(ctx, t, vs, tmproto.PrevoteType, stateData.state.ChainID,
			types.BlockID{}, stateData.Validators.QuorumType, stateData.Validators.QuorumHash))
	}
	return votes
}

// A node can reach a round by following +2/3 prevotes of it, without the
// certificate for the round before. It still takes that certificate from a
// peer, keeps it and passes it on, for peers still in the skipped round:
// nothing else moves them on, and this node may be their only link to the
// validators that hold it.
func TestStateKeepsTheCertificateOfARoundItLeftByPrevotes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ctx, cs, vss := newRoundSkipCatchupState(ctx, t)
	stateData := cs.GetStateData()
	height := stateData.Height
	certificate := recoverRoundSkipCertificate(ctx, t, cs, vss, height, 0)

	var broadcast []*types.RoundSkipCertificate
	cs.emitter.AddListener(roundSkipCertificateEventName, func(data eventemitter.EventData) error {
		broadcast = append(broadcast, data.(*types.RoundSkipCertificate))
		return nil
	})

	for _, vote := range nilPrevotes(ctx, t, cs, vss, 1) {
		mi := msgInfo{Msg: &VoteMessage{Vote: vote}, PeerID: "peer"}
		require.NoError(t, cs.ctrl.Dispatch(msgInfoWithCtx(ctx, mi), &AddVoteEvent{Vote: vote, PeerID: mi.PeerID}, &stateData))
	}
	require.Equal(t, int32(1), stateData.Round, "the prevotes of round 1 took the node there")
	require.Nil(t, stateData.RoundSkip)

	require.True(t, roundSkipCertificateWanted(&stateData, certificate))
	require.NoError(t, cs.msgDispatcher.dispatch(ctx, &stateData,
		msgInfo{Msg: &RoundSkipCertificateMessage{Certificate: certificate}, PeerID: "peer"}))

	assert.Equal(t, int32(1), stateData.Round, "the node stays in its round")
	require.NotNil(t, stateData.RoundSkip, "the certificate is kept")
	assert.Equal(t, int32(0), stateData.RoundSkip.Round)
	assert.Equal(t, []*types.RoundSkipCertificate{certificate}, broadcast, "and broadcast")
	assert.False(t, roundSkipCertificateWanted(&stateData, certificate), "another copy is not news")

	var sent []proto.Message
	behind := &PeerState{PRS: cstypes.PeerRoundState{Height: height, Round: 0, Step: cstypes.RoundStepNewRound}}
	roundSkipGossipHandler(behind, log.NewNopLogger(), func(_ context.Context, msg proto.Message) error {
		sent = append(sent, msg)
		return nil
	})(ctx, stateData)
	require.Len(t, sent, 1, "a peer still in round 0 is sent it")
	assert.Equal(t, *certificate.ToProto(), sent[0].(*tmcons.RoundSkipCertificate).Certificate)
}

// A peer's certificate reaches the WAL only once it has been verified, and
// before applying it writes anything. Replay checks every certificate in the
// WAL again, drawing on no verification budget, so the certificates a peer
// can send that look like news and do not verify must not pile up there.
func TestPeerCertificateIsWrittenToTheWALOnlyOnceVerified(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ctx, cs, vss := newRoundSkipCatchupState(ctx, t)
	wal := &recordingWAL{}
	cs.wal = wal
	stateData := cs.GetStateData()
	height := stateData.Height

	signedForRound3 := recoverRoundSkipCertificate(ctx, t, cs, vss, height, 3)
	for round := int32(0); round < 3; round++ {
		forged := *signedForRound3
		forged.Round = round
		require.True(t, roundSkipCertificateWanted(&stateData, &forged))
		// The refusal is logged, not returned.
		require.NoError(t, cs.msgDispatcher.dispatch(ctx, &stateData,
			msgInfo{Msg: &RoundSkipCertificateMessage{Certificate: &forged}, PeerID: "peer"}))
		require.Nil(t, stateData.RoundSkip, "a certificate signed for round 3 was taken for round %d", round)
		require.Equal(t, int32(0), stateData.Round)
	}
	assert.Zero(t, wal.count(), "a certificate that did not verify was written")

	mi := msgInfo{Msg: &RoundSkipCertificateMessage{Certificate: recoverRoundSkipCertificate(ctx, t, cs, vss, height, 0)},
		PeerID: "peer"}
	require.NoError(t, cs.msgDispatcher.dispatch(ctx, &stateData, mi))
	require.Equal(t, int32(1), stateData.Round)
	require.NotZero(t, wal.count())
	assert.Equal(t, mi, wal.msgs[0], "the certificate is written before the round it starts")
	certificates := 0
	for _, record := range wal.msgs {
		if record, ok := record.(msgInfo); ok {
			if _, ok := record.Msg.(*RoundSkipCertificateMessage); ok {
				certificates++
			}
		}
	}
	assert.Equal(t, 1, certificates)
}

// Replay dispatches a certificate again under the peer that sent it, long
// after the fact, and a certificate naming another quorum is what a peer with
// another validator set sends. Neither has its sender reported.
func TestRefusedCertificatesThatAreNoPeerFault(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ctx, cs, vss := newRoundSkipCatchupState(ctx, t)
	stateData := cs.GetStateData()
	height := stateData.Height

	forged := recoverRoundSkipCertificate(ctx, t, cs, vss, height, 3)
	forged.Round = 0
	require.NoError(t, cs.msgDispatcher.dispatch(ctx, &stateData,
		msgInfo{Msg: &RoundSkipCertificateMessage{Certificate: forged}, PeerID: "peer"}, msgFromReplay()))

	otherQuorum := recoverRoundSkipCertificate(ctx, t, cs, vss, height, 0)
	otherQuorum.QuorumHash = crypto.RandQuorumHash()
	require.NoError(t, cs.msgDispatcher.dispatch(ctx, &stateData,
		msgInfo{Msg: &RoundSkipCertificateMessage{Certificate: otherQuorum}, PeerID: "peer"}))

	require.Nil(t, stateData.RoundSkip)
	require.Equal(t, int32(0), stateData.Round)
	assert.Empty(t, cs.peerErrorQueue.ch)
}

// The iterator that skips rounds replays only the certificates of the rounds
// it skips, not their prevotes. A node that crashed just after a certificate
// moved it into the propose step of the next round, because the round before
// had a polka, must not then come back waiting in that round for transactions:
// it would have no timeout running, and the round's proposer's heartbeats
// would keep it there.
func TestReplayDoesNotWaitInARoundTheNodeLeftForPropose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	test := newRoundSkipTest(ctx, t, "consensus_replay_round_skip_polka_test", nil)
	committed := test.cs.GetStateData().state
	const height = int64(2)

	// Round 0 of height 2: the three other validators prevote a block whose
	// parts never reach the node, and a certificate skips the round. The node
	// enters the propose step of round 1, and crashes.
	blockID := factory.MakeBlockID()
	for _, vs := range test.vss[1:] {
		vs.Height, vs.Round = height, 0
	}
	prevotes := signVotes(ctx, t, tmproto.PrevoteType, test.chainID, blockID, nil,
		committed.Validators.QuorumType, committed.Validators.QuorumHash, test.vss[1:]...)
	certificate := recoverRoundSkipCertificate(ctx, t, test.cs, test.vss, height, 0)
	wal := make([]any, 0, 5+len(prevotes))
	wal = append(wal,
		EndHeightMessage{Height: height - 1},
		newEventDataRoundState(height, 0, cstypes.RoundStepNewHeight),
	)
	for _, vote := range prevotes {
		wal = append(wal, msgInfo{Msg: &VoteMessage{Vote: vote}, PeerID: "peer"})
	}
	wal = append(wal,
		msgInfo{Msg: &RoundSkipCertificateMessage{Certificate: certificate}, PeerID: "peer"},
		newEventDataRoundState(height, 1, cstypes.RoundStepNewRound),
		newEventDataRoundState(height, 1, cstypes.RoundStepPropose),
	)

	for _, skipRounds := range []bool{true, false} {
		t.Run(fmt.Sprintf("skip rounds %v", skipRounds), func(t *testing.T) {
			config, err := ResetConfig(t, "consensus_replay_round_skip_polka_restart_test")
			require.NoError(t, err)
			config.Consensus.CreateEmptyBlocks = false
			config.Consensus.WalSkipRoundsToLast = skipRounds
			replay := newStateWithConfigAndBlockStore(ctx, t, log.NewNopLogger(), config, committed,
				test.cs.privValidator.PrivValidator, NewCounterApplication(), test.cs.blockStore)
			replay.emitter.Emit(setReplayModeEventName, true)
			ctx := dash.ContextWithProTxHash(ctx, replay.privValidator.ProTxHash)
			require.Equal(t, height, replay.GetStateData().Height)

			iter := newWalIter(&mockWALReader{ret: wal}, skipRounds)
			for iter.Next() {
				require.NoError(t, replay.readReplayMessage(ctx, iter.Value(), nil))
			}
			require.NoError(t, iter.Err())

			stateData := replay.GetStateData()
			require.NotNil(t, stateData.RoundSkip)
			assert.Equal(t, int32(1), stateData.Round)
			assert.Equal(t, cstypes.RoundStepPropose, stateData.Step,
				"replay left the node waiting in a round it had left for the propose step")
		})
	}
}

// A certificate hands the round over on the consensus goroutine, which
// announces the new round to peers. That must not wait for the router, where a
// peer that stops reading can hold up every send on the state channel. Nor may
// the announcement be lost when the node has more to announce than the reactor
// holds: peers send a node a proposal by the round they last heard it is in.
func TestRoundSkipHandoffDoesNotWaitForTheStateChannel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ctx, cs, vss := newRoundSkipCatchupState(ctx, t)
	stateData := cs.GetStateData()
	height := stateData.Height
	certificate := recoverRoundSkipCertificate(ctx, t, cs, vss, height, 0)

	ch := newHeldChannel()
	r := &Reactor{
		logger:          log.NewNopLogger(),
		state:           cs,
		livenessOutbox:  make(chan livenessSend, livenessOutboxSize),
		stateOutbox:     make(chan proto.Message, stateOutboxSize),
		stateOutboxWake: make(chan struct{}, 1),
	}
	r.subscribeToBroadcastEvents()
	go r.sendState(ctx, ch)

	handedOver := make(chan error, 1)
	go func() {
		// More votes to announce than the reactor holds, so the new round's
		// announcement finds no room either.
		for i := 0; i <= stateOutboxSize+1; i++ {
			cs.emitter.Emit(types.EventVoteValue, &types.Vote{
				Height: height, Type: tmproto.PrevoteType, ValidatorIndex: int32(i % len(vss)),
			})
		}
		handedOver <- cs.msgDispatcher.dispatch(ctx, &stateData,
			msgInfo{Msg: &RoundSkipCertificateMessage{Certificate: certificate}, PeerID: "peer"})
	}()
	select {
	case err := <-handedOver:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the round handoff waited for the state channel")
	}
	require.Equal(t, int32(1), stateData.Round)
	require.Equal(t, int32(1), cs.GetStateData().Round)

	close(ch.release)
	require.Eventually(t, func() bool {
		step, ok := ch.last().(*tmcons.NewRoundStep)
		return ok && step.Height == height && step.Round == 1
	}, 10*time.Second, 10*time.Millisecond, "peers hear of the new round once the channel takes messages again")
}

// A broadcast that finds the outbox full is recorded as dropped after the
// failed send, and the sender may have caught up and gone to sleep in between.
// The record must wake it with nothing else queued: the dropped message may
// have been the last round step before an idle wait.
func TestStateSenderWakesForADropRecordedAfterItCaughtUp(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ctx, cs, _ := newRoundSkipCatchupState(ctx, t)
	height := cs.GetStateData().Height

	ch := newHeldChannel()
	close(ch.release)
	r := &Reactor{
		logger:          log.NewNopLogger(),
		state:           cs,
		stateOutbox:     make(chan proto.Message, 1),
		stateOutboxWake: make(chan struct{}, 1),
	}
	go r.sendState(ctx, ch)

	r.queueState(&tmcons.HasVote{Height: height})
	require.Eventually(t, func() bool { return ch.len() == 1 }, 10*time.Second, time.Millisecond,
		"the sender took what was queued")
	// Long enough for the sender to find the outbox empty and go to sleep.
	time.Sleep(50 * time.Millisecond)

	r.recordStateDrop()
	require.Eventually(t, func() bool {
		step, ok := ch.last().(*tmcons.NewRoundStep)
		return ok && step.Height == height
	}, 10*time.Second, time.Millisecond, "the sender woke and sent the round step again")
}

// heldChannel is a p2p.Channel that takes nothing until it is released, as the
// router's channel does while a peer it queues for has stopped reading. Then it
// records what it takes.
type heldChannel struct {
	p2p.Channel
	release chan struct{}
	mtx     sync.Mutex
	sent    []proto.Message
}

func newHeldChannel() *heldChannel { return &heldChannel{release: make(chan struct{})} }

func (ch *heldChannel) Send(ctx context.Context, envelope p2p.Envelope) error {
	select {
	case <-ch.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	ch.mtx.Lock()
	defer ch.mtx.Unlock()
	ch.sent = append(ch.sent, envelope.Message)
	return nil
}

// len returns how many messages the channel took.
func (ch *heldChannel) len() int {
	ch.mtx.Lock()
	defer ch.mtx.Unlock()
	return len(ch.sent)
}

// last returns the last message the channel took, or nil.
func (ch *heldChannel) last() proto.Message {
	ch.mtx.Lock()
	defer ch.mtx.Unlock()
	if len(ch.sent) == 0 {
		return nil
	}
	return ch.sent[len(ch.sent)-1]
}

// A peer that sent this node a certificate holds it, and is not sent it back.
func TestReactorRecordsTheCertificateAPeerSent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	f := newLivenessReactorFixture(ctx, t)
	certificate := recoverRoundSkipCertificate(ctx, t, f.cs, f.vss, f.stateData.Height, f.stateData.Round)

	f.send(&tmcons.RoundSkipCertificate{Certificate: *certificate.ToProto()})
	f.refused(ctx, t)

	ps, ok := f.r.GetPeerState(livenessTestPeer)
	require.True(t, ok)
	assert.True(t, ps.HasRoundSkip(certificate.Height, certificate.Round))

	holding := f.stateData
	holding.RoundSkip = certificate
	holding.Round = certificate.Round + 1
	sent := 0
	roundSkipGossipHandler(ps, log.NewNopLogger(), func(context.Context, proto.Message) error {
		sent++
		return nil
	})(ctx, holding)
	assert.Zero(t, sent)
}

// Replay that skips to the last round of a height keeps the certificates of
// the rounds it skips. A node that held one for peers still behind holds it
// again after a restart, though it reached the round it replays to by
// following that round's prevotes.
func TestReplayKeepsTheCertificateOfASkippedRound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ctx, source, vss := newRoundSkipCatchupState(ctx, t)
	original := source.GetStateData()
	height := original.Height
	certificate := recoverRoundSkipCertificate(ctx, t, source, vss, height, 0)

	prevotes := nilPrevotes(ctx, t, source, vss, 1)
	wal := make([]any, 0, 5+len(prevotes))
	wal = append(wal,
		EndHeightMessage{Height: height - 1},
		newEventDataRoundState(height, 0, cstypes.RoundStepNewHeight),
		msgInfo{Msg: &RoundSkipCertificateMessage{Certificate: certificate}, PeerID: "peer"},
		newEventDataRoundState(height, 1, cstypes.RoundStepNewRound),
		newEventDataRoundState(height, 1, cstypes.RoundStepPropose),
	)
	for _, vote := range prevotes {
		wal = append(wal, msgInfo{Msg: &VoteMessage{Vote: vote}, PeerID: "peer"})
	}

	for _, skipRounds := range []bool{true, false} {
		t.Run(fmt.Sprintf("skip rounds %v", skipRounds), func(t *testing.T) {
			replay := newState(ctx, t, log.NewNopLogger(), original.state.Copy(),
				source.privValidator.PrivValidator, NewCounterApplication())
			replay.config.DontAutoPropose = true
			replay.config.WalSkipRoundsToLast = skipRounds
			replay.emitter.Emit(setReplayModeEventName, true)

			iter := newWalIter(&mockWALReader{ret: wal}, skipRounds)
			for iter.Next() {
				require.NoError(t, replay.readReplayMessage(ctx, iter.Value(), nil))
			}
			require.NoError(t, iter.Err())

			stateData := replay.GetStateData()
			assert.Equal(t, int32(1), stateData.Round)
			assert.True(t, stateData.Votes.Prevotes(1).HasTwoThirdsAny())
			require.NotNil(t, stateData.RoundSkip, "the certificate survived replay")
			assert.Equal(t, int32(0), stateData.RoundSkip.Round)
		})
	}
}
