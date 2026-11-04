package consensus

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
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
