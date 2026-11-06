package statesync

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	sm "github.com/dashpay/tenderdash/internal/state"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
	"github.com/dashpay/tenderdash/version"
)

// TestSetLastBlockRandomness checks the state restored from a snapshot taken at a block committed at
// consensus version 2: its LastAppHash is the app hash the commit signs and its LastBlockBeacon the
// commit's verified beacon. Before consensus version 2, or when the snapshot block's header does not
// commit to the next height's parameters, the state is left as it is.
func TestSetLastBlockRandomness(t *testing.T) {
	ctx := context.Background()
	const chainID = "test-chain"
	const round = int32(1)
	valSet, privVals := types.RandValidatorSet(4)
	params := *types.DefaultConsensusParams()
	params.Version.ConsensusVersion = int32(tmproto.VersionParams_CONSENSUS_VERSION_2)

	header := &types.Header{
		Version:            version.Consensus{Block: version.BlockProtocol, App: 1},
		ChainID:            chainID,
		Height:             10,
		Time:               time.Now(),
		ValidatorsHash:     valSet.Hash(),
		NextValidatorsHash: valSet.Hash(),
		ConsensusHash:      params.HashConsensusParams(),
		AppHash:            crypto.Checksum([]byte("header app hash")),
		ProposerProTxHash:  valSet.Validators[0].ProTxHash,
	}
	prevoteBlockID := types.BlockID{
		Hash:          header.Hash(),
		PartSetHeader: types.PartSetHeader{Total: 1, Hash: crypto.Checksum([]byte("parts"))},
		StateID:       header.StateID().Hash(),
	}

	voteSet := types.NewVoteSet(chainID, header.Height, round, tmproto.PrevoteType, valSet, types.WithRandomBeacon(true))
	for i, privVal := range privVals {
		proTxHash, err := privVal.GetProTxHash(ctx)
		require.NoError(t, err)
		vote := &types.Vote{
			ValidatorProTxHash: proTxHash,
			ValidatorIndex:     int32(i),
			Height:             header.Height,
			Round:              round,
			Type:               tmproto.PrevoteType,
			BlockID:            prevoteBlockID,
		}
		protoVote := vote.ToProto()
		require.NoError(t, privVal.SignVote(ctx, chainID, valSet.QuorumType, valSet.QuorumHash, protoVote, nil))
		require.NoError(t, vote.PopulateSignsFromProto(protoVote))
		_, err = voteSet.AddVote(vote)
		require.NoError(t, err)
	}
	beacon := voteSet.ThresholdBlockSignature()
	require.Len(t, beacon, types.SignatureSize)

	finalAppHash := tmbytes.HexBytes(crypto.Checksum(append(header.AppHash.Copy(), beacon...)))
	commitBlockID := prevoteBlockID.Copy()
	commitBlockID.StateID = header.StateIDWithAppHash(finalAppHash).Hash()
	lightBlock := func(beacon []byte) *types.LightBlock {
		return &types.LightBlock{
			SignedHeader: &types.SignedHeader{Header: header, Commit: &types.Commit{
				Height:  header.Height,
				Round:   round,
				BlockID: commitBlockID,
				Beacon:  beacon,
				AppHash: finalAppHash,
			}},
			ValidatorSet: valSet,
		}
	}
	newState := func(consensusVersion int32) sm.State {
		p := params
		p.Version.ConsensusVersion = consensusVersion
		return sm.State{ChainID: chainID, LastValidators: valSet, ConsensusParams: p,
			LastAppHash: crypto.Checksum([]byte("next header app hash"))}
	}

	t.Run("consensus version 2", func(t *testing.T) {
		state := newState(2)
		require.NoError(t, setLastBlockRandomness(&state, lightBlock(beacon)))
		require.EqualValues(t, finalAppHash, state.LastAppHash)
		require.Equal(t, beacon, state.LastBlockBeacon)
	})

	t.Run("a beacon that does not verify", func(t *testing.T) {
		state := newState(2)
		require.ErrorIs(t, setLastBlockRandomness(&state, lightBlock(crypto.CRandBytes(types.SignatureSize))),
			types.ErrInvalidCommitBeacon)
	})

	for name, consensusVersion := range map[string]int32{
		"before consensus version 2": 1,
		// The next height uses version 2, but the snapshot header commits to other hashed parameters, as at
		// the activation height.
		"activation height": 2,
	} {
		t.Run(name, func(t *testing.T) {
			state := newState(consensusVersion)
			if name == "activation height" {
				state.ConsensusParams.Block.MaxBytes++
			}
			before := state.LastAppHash
			require.NoError(t, setLastBlockRandomness(&state, lightBlock(beacon)))
			require.Equal(t, before, state.LastAppHash)
			require.Empty(t, state.LastBlockBeacon)
		})
	}
}
