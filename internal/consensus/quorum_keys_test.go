package consensus

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/crypto"
	dashcore "github.com/dashpay/tenderdash/dash/core"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
	pv "github.com/dashpay/tenderdash/privval"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func TestQuorumKeyRefreshRestoresProposalAndVotes(t *testing.T) {
	ctx := context.Background()
	complete, keys := factory.MockValidatorSet()
	local := complete.Copy()
	for _, val := range local.Validators {
		val.PubKey = nil
	}
	local.HasPublicKeys = false
	params := *types.DefaultConsensusParams()
	params.Version.ConsensusVersion = 2
	params.Validator.VotingPowerThreshold = &complete.VotingPowerThreshold
	base := sm.State{ChainID: "quorum-key-refresh", InitialHeight: 1, Validators: local, ConsensusParams: params}
	core := dashcore.NewMockClient(base.ChainID, complete.QuorumType, keys[0], true)
	core.SetValidatorSet(complete)
	signer, err := pv.NewDashCoreSignerClient(core, complete.QuorumType, log.NewNopLogger())
	require.NoError(t, err)
	proposer := &Proposaler{logger: log.NewTestingLogger(t), metrics: NopMetrics(),
		privVal: privValidator{PrivValidator: signer, ProTxHash: complete.Validators[0].ProTxHash}, committedState: base}
	state := StateData{logger: log.NewNopLogger(), metrics: NopMetrics()}
	state.updateToState(base, nil, nil)
	makeProposal := func() *types.Proposal {
		proposal := types.NewProposal(state.Height, 1, 0, -1, factory.MakeBlockID(), time.Now().UTC())
		require.NoError(t, proposer.signProposal(ctx, state.Height, proposal))
		return proposal
	}
	proposal := makeProposal()
	require.ErrorIs(t, proposer.Set(ctx, proposal, proposal.Timestamp, &state.RoundState), ErrUnableToVerifyProposal)
	require.Nil(t, state.Proposal)
	require.False(t, shouldProposalBeGossiped(state.RoundState, &cstypes.PeerRoundState{}, true))
	refresh, err := sm.NewCurrentRoundState(complete.Validators[0].ProTxHash,
		sm.RoundParams{Source: sm.ProcessProposalSource, ValidatorSetUpdate: complete.ABCIEquivalentValidatorUpdates()}, base)
	require.NoError(t, err)
	require.NoError(t, refresh.UpdateState(&base))
	// Recovery becomes active at the next committed height, rebuilding the vote sets.
	base.LastBlockHeight = 1
	state.updateToState(base, &types.Commit{Height: 1}, nil)
	proposer.committedState = base
	proposal = makeProposal()
	require.NoError(t, proposer.Set(ctx, proposal, proposal.Timestamp, &state.RoundState))
	require.NotNil(t, state.Proposal)
	require.True(t, shouldProposalBeGossiped(state.RoundState, &cstypes.PeerRoundState{}, true))
	for i, key := range keys {
		vote := &types.Vote{Type: tmproto.PrevoteType, Height: state.Height, Round: 0,
			ValidatorIndex: int32(i), ValidatorProTxHash: complete.Validators[i].ProTxHash}
		proto := vote.ToProto()
		require.NoError(t, key.SignVote(ctx, base.ChainID, complete.QuorumType, complete.QuorumHash, proto, log.NewNopLogger()))
		vote.BlockSignature = proto.BlockSignature
		added, err := state.Votes.AddVote(vote)
		require.NoError(t, err)
		require.True(t, added)
	}
	require.True(t, state.Votes.Prevotes(0).HasTwoThirdsMajority())
	state.Proposal = nil
	proposal.Signature[0] ^= 1
	require.ErrorIs(t, proposer.Set(ctx, proposal, proposal.Timestamp, &state.RoundState), ErrInvalidProposalSignature)
	require.Nil(t, state.Proposal)
}

func TestValidatorMissingQuorumKeysReadiness(t *testing.T) {
	var output bytes.Buffer
	logger, err := log.NewLogger("error", &output)
	require.NoError(t, err)
	metrics := NopMetrics()
	missingKeys := &recordingGauge{}
	metrics.ValidatorMissingQuorumKeys = missingKeys
	emitter := eventemitter.New()
	store := NewStateDataStore(metrics, logger, config.DefaultConsensusConfig(), emitter)
	store.Subscribe(emitter)
	complete, _ := factory.MockValidatorSet()
	missing := complete.Copy()
	for _, val := range missing.Validators {
		val.PubKey = nil
	}
	missing.HasPublicKeys = false
	set := func(vals *types.ValidatorSet, height int64) {
		data := store.Get()
		data.RoundState = cstypes.RoundState{Validators: vals, Height: height}
		data.state = sm.State{Validators: vals, LastBlockHeight: height - 1}
		require.NoError(t, store.Update(data))
	}
	gauge := func(want float64) {
		t.Helper()
		require.True(t, missingKeys.set)
		require.Equal(t, want, missingKeys.value)
	}
	set(missing, 10)
	require.Empty(t, output.String(), "observers do not require member shares")
	gauge(0)
	emitter.Emit(setPrivValidatorEventName, privValidator{ProTxHash: complete.Validators[0].ProTxHash})
	require.NotEmpty(t, output.String(), "installing the local identity must report missing keys")
	var entry map[string]interface{}
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(output.Bytes()), &entry))
	require.Equal(t, "error", entry["level"])
	require.Contains(t, entry["message"], "missing quorum member public keys")
	require.Equal(t, missing.QuorumHash.String(), entry["quorum_hash"])
	require.EqualValues(t, 10, entry["height"])
	gauge(1)
	for height := int64(11); height < 14; height++ {
		set(missing, height)
	}
	require.Equal(t, 1, strings.Count(output.String(), "\n"), "one log per continuous missing-key range")
	set(complete, 14)
	gauge(0)
	set(missing, 15)
	gauge(1)
	require.Equal(t, 2, strings.Count(output.String(), "\n"))
	rotated := missing.Copy()
	rotated.QuorumHash = crypto.RandQuorumHash()
	set(rotated, 16)
	require.Equal(t, 3, strings.Count(output.String(), "\n"))
	emitter.Emit(setPrivValidatorEventName, privValidator{ProTxHash: crypto.RandProTxHash()})
	gauge(0)
	emitter.Emit(setPrivValidatorEventName, privValidator{})
	gauge(0)
	require.Equal(t, 3, strings.Count(output.String(), "\n"))
}
