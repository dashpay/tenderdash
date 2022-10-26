package types

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/tendermint/tendermint/crypto"
	tmrand "github.com/tendermint/tendermint/libs/rand"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
)

const StateIDVersion = 1

func RandStateID() StateID {
	return StateID{
		Height:                uint64(rand.Int63()), //nolint:gosec
		AppHash:               tmrand.Bytes(crypto.DefaultAppHashSize),
		AppVersion:            StateIDVersion,
		CoreChainLockedHeight: rand.Uint32(), //nolint:gosec
		Time:                  time.Now(),
	}
}

func makeCommit(
	ctx context.Context,
	blockID BlockID,
	height int64,
	round int32,
	voteSet *VoteSet,
	validators []PrivValidator,
) (*Commit, error) {

	// all sign
	for i := 0; i < len(validators); i++ {
		proTxHash, err := validators[i].GetProTxHash(ctx)
		if err != nil {
			return nil, fmt.Errorf("can't get proTxHash: %w", err)
		}
		vote := &Vote{
			ValidatorProTxHash: proTxHash,
			ValidatorIndex:     int32(i),
			Height:             height,
			Round:              round,
			Type:               tmproto.PrecommitType,
			BlockID:            blockID,
			VoteExtensions: VoteExtensions{
				tmproto.VoteExtensionType_DEFAULT:           []VoteExtension{{Extension: []byte("default")}},
				tmproto.VoteExtensionType_THRESHOLD_RECOVER: []VoteExtension{{Extension: []byte("threshold")}},
			},
		}

		_, err = signAddVote(ctx, validators[i], vote, voteSet)
		if err != nil {
			return nil, err
		}
	}

	return voteSet.MakeCommit(), nil
}

// signAddVote signs a vote using StateID configured inside voteSet, and adds it to that voteSet
func signAddVote(ctx context.Context, privVal PrivValidator, vote *Vote, voteSet *VoteSet) (signed bool, err error) {
	v := vote.ToProto()
	err = privVal.SignVote(ctx, voteSet.ChainID(), voteSet.valSet.QuorumType, voteSet.valSet.QuorumHash,
		v, nil)
	if err != nil {
		return false, err
	}
	err = vote.PopulateSignsFromProto(v)
	if err != nil {
		return false, err
	}
	return voteSet.AddVote(vote)
}
