package factory

import (
	"context"

	"github.com/dashpay/tenderdash/libs/math"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func MakeVote(
	ctx context.Context,
	val types.PrivValidator,
	valSet *types.ValidatorSet,
	chainID string,
	valIndex int32,
	height int64,
	round int32,
	step int,
	blockID types.BlockID,
) (*types.Vote, error) {
	proTxHash, err := val.GetProTxHash(ctx)
	if err != nil {
		return nil, err
	}

	v := &types.Vote{
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     valIndex,
		Height:             height,
		Round:              round,
		Type:               tmproto.SignedMsgType(math.MustConvertInt32(step)),
		BlockID:            blockID,
	}

	vpb := v.ToProto()

	if err := val.SignVote(ctx, chainID, valSet.QuorumType, valSet.QuorumHash, vpb, nil); err != nil {
		return nil, err
	}

	v.BlockSignature = vpb.BlockSignature

	err = v.VoteExtensions.CopySignsFromProto(vpb.VoteExtensions)
	if err != nil {

		return nil, err
	}
	return v, nil
}
