package types

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/dashpay/tenderdash/crypto"
	tmrand "github.com/dashpay/tenderdash/libs/rand"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

const StateIDVersion = 1

func RandStateID() tmproto.StateID {
	return tmproto.StateID{
		Height:                uint64(rand.Int63()), //nolint:gosec
		AppHash:               tmrand.Bytes(crypto.DefaultAppHashSize),
		AppVersion:            StateIDVersion,
		CoreChainLockedHeight: rand.Uint32(), //nolint:gosec
		Time:                  uint64(tmtime.Now().UnixMilli()),
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
				tmproto.VoteExtensionType_DEFAULT: []tmproto.VoteExtension{{
					Type:      tmproto.VoteExtensionType_DEFAULT,
					Extension: []byte("default")}},
				tmproto.VoteExtensionType_THRESHOLD_RECOVER: []tmproto.VoteExtension{{
					Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
					Extension: []byte("threshold")}},
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

	vote.VoteExtensions = VoteExtensionsFromProto(v.VoteExtensions)

	return voteSet.AddVote(vote)
}
