package types

import (
	"context"
	"testing"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/crypto"
	tmrand "github.com/dashpay/tenderdash/libs/rand"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func TestPeerCatchupRounds(t *testing.T) {
	cfg, err := config.ResetTestRoot(t.TempDir(), "consensus_height_vote_set_test")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	valSet, privVals := types.RandValidatorSet(10)

	stateID := tmproto.StateID{Height: 1}

	chainID := cfg.ChainID()
	hvs := NewHeightVoteSet(chainID, 1, valSet)

	vote999_0 := makeVoteHR(ctx, t, 1, 0, 999, privVals, chainID, valSet.QuorumType, valSet.QuorumHash, stateID)
	added, err := hvs.AddVote(vote999_0)
	if !added || err != nil {
		t.Error("Expected to successfully add vote from peer", added, err)
	}

	vote1000_0 := makeVoteHR(ctx, t, 1, 0, 1000, privVals, chainID, valSet.QuorumType, valSet.QuorumHash, stateID)
	added, err = hvs.AddVote(vote1000_0)
	if !added || err != nil {
		t.Error("Expected to successfully add vote from peer", added, err)
	}

	vote1001_0 := makeVoteHR(ctx, t, 1, 0, 1001, privVals, chainID, valSet.QuorumType, valSet.QuorumHash, stateID)
	added, err = hvs.AddVote(vote1001_0)
	if err != ErrGotVoteFromUnwantedRound {
		t.Errorf("expected GotVoteFromUnwantedRoundError, but got %v", err)
	}
	if added {
		t.Error("Expected to *not* add vote from peer, too many catchup rounds.")
	}
}

func makeVoteHR(
	ctx context.Context,
	t *testing.T,
	height int64,
	valIndex, round int32,
	privVals []types.PrivValidator,
	chainID string,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	stateID tmproto.StateID,
) *types.Vote {
	privVal := privVals[valIndex]
	proTxHash, err := privVal.GetProTxHash(ctx)
	require.NoError(t, err)

	randBytes := tmrand.Bytes(crypto.HashSize)

	vote := &types.Vote{
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     valIndex,
		Height:             height,
		Round:              round,
		Type:               tmproto.PrecommitType,
		BlockID: types.BlockID{
			Hash:          randBytes,
			PartSetHeader: types.PartSetHeader{},
			StateID:       stateID.Hash(),
		},
	}

	v := vote.ToProto()
	err = privVal.SignVote(ctx, chainID, quorumType, quorumHash, v, nil)
	require.NoError(t, err, "Error signing vote")

	vote.BlockSignature = v.BlockSignature
	err = vote.VoteExtensions.CopySignsFromProto(v.VoteExtensions)
	require.NoError(t, err)

	return vote
}
