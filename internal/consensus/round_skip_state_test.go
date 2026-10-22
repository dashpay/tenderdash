package consensus

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
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

// deliverRoundSkipVote does with a peer's skip vote what the reactor does.
func deliverRoundSkipVote(ctx context.Context, t *testing.T, cs *State, vote *types.RoundSkipVote) {
	t.Helper()
	_, certificate, err := cs.roundSkips.add(vote, true)
	require.NoError(t, err)
	if certificate != nil {
		require.NoError(t, cs.sendMessage(ctx, &RoundSkipCertificateMessage{Certificate: certificate}, "peer"))
	}
}

func TestStateRoundSkip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	baseConfig := configSetup(t)
	config, err := ResetConfig(t, "consensus_round_skip_test")
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
	cs := newStateWithConfig(ctx, t, log.NewNopLogger(), config, state, privVals[0], NewCounterApplication())
	vss := make([]*validatorStub, len(privVals))
	for i, pv := range privVals {
		vss[i] = newValidatorStub(pv, int32(i), state.InitialHeight)
	}

	newRoundCh := subscribe(ctx, t, cs.eventBus, types.EventQueryNewRound)
	proposalCh := subscribe(ctx, t, cs.eventBus, types.EventQueryCompleteProposal)
	ownSkipVotes := make(chan *types.RoundSkipVote, 10)
	cs.emitter.AddListener(roundSkipVoteEventName, func(data eventemitter.EventData) error {
		ownSkipVotes <- data.(*types.RoundSkipVote)
		return nil
	})
	heartbeats := make(chan *types.ProposerHeartbeat, 1000)
	cs.emitter.AddListener(proposerHeartbeatEventName, func(data eventemitter.EventData) error {
		heartbeats <- data.(*types.ProposerHeartbeat)
		return nil
	})

	// Height 1: this node proposes and the stubs commit its block.
	startTestRound(ctx, cs, 1, 0)
	ensureNewRound(t, newRoundCh, 1, 0)
	ensureNewProposal(t, proposalCh, 1, 0)
	rs := cs.GetRoundState()
	signAddVotes(ctx, t, cs, tmproto.PrecommitType, config.ChainID(), rs.BlockID(), vss[1:]...)
	ensureNewRound(t, newRoundCh, 2, 0)

	// Height 2: the proposers of rounds 0, 1 and 2 are stubs, which send no
	// heartbeats. In each round this node votes to skip, two validators other
	// than the silent proposer join it, and the certificate moves it on.
	silentProposers := []int{1, 2, 3}
	for round, silent := range silentProposers {
		round := int32(round)
		select {
		case vote := <-ownSkipVotes:
			assert.Equal(t, int64(2), vote.Height)
			assert.Equal(t, round, vote.Round)
		case <-time.After(5 * time.Second):
			t.Fatalf("no skip vote for silent round %d", round)
		}
		for i := 1; i < len(vss); i++ {
			if i != silent {
				deliverRoundSkipVote(ctx, t, cs, signRoundSkipVote(ctx, t, cs, vss[i], 2, round))
			}
		}
		ensureNewRound(t, newRoundCh, 2, round+1)

		stateData := cs.GetStateData()
		require.NotNil(t, stateData.RoundSkip)
		assert.Equal(t, round, stateData.RoundSkip.Round)
	}

	// Round 3 is this node's. It keeps waiting, now as the proposer, and
	// announces itself; no block is made before there is something to propose.
	deadline := time.After(5 * time.Second)
	for heard := false; !heard; {
		select {
		case hb := <-heartbeats:
			heard = hb.Height == 2 && hb.Round == 3
		case <-deadline:
			t.Fatal("no heartbeat from the proposer of round 3")
		}
	}
	stateData := cs.GetStateData()
	assert.Equal(t, int32(3), stateData.Round)
	assert.Equal(t, cstypes.RoundStepNewRound, stateData.Step)
	ensureNoNewEventOnChannel(t, proposalCh)

	checkTxsRange(ctx, t, cs, 0, 1)
	ensureNewProposal(t, proposalCh, 2, 3)
}
