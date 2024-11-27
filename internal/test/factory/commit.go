package factory

import (
	"context"

	tmmath "github.com/dashpay/tenderdash/libs/math"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func MakeCommit(
	ctx context.Context,
	blockID types.BlockID,
	height int64,
	round int32,
	voteSet *types.VoteSet,
	validatorSet *types.ValidatorSet,
	validators []types.PrivValidator,
) (*types.Commit, error) {
	// all sign
	for i := 0; i < len(validators); i++ {
		proTxHash, err := validators[i].GetProTxHash(ctx)
		if err != nil {
			return nil, err
		}
		vote := &types.Vote{
			ValidatorProTxHash: proTxHash,
			ValidatorIndex:     tmmath.MustConvertInt32(i),
			Height:             height,
			Round:              round,
			Type:               tmproto.PrecommitType,
			BlockID:            blockID,
		}

		v := vote.ToProto()

		if err := validators[i].SignVote(ctx, voteSet.ChainID(), validatorSet.QuorumType, validatorSet.QuorumHash, v, nil); err != nil {
			return nil, err
		}
		vote.BlockSignature = v.BlockSignature

		err = vote.VoteExtensions.CopySignsFromProto(v.VoteExtensions)
		if err != nil {
			return nil, err
		}

		if _, err := voteSet.AddVote(vote); err != nil {
			return nil, err
		}
	}

	return voteSet.MakeCommit(), nil
}
