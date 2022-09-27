package factory

import (
	"context"

	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
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
	appHash tmbytes.HexBytes,
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
		Type:               tmproto.SignedMsgType(step),
		BlockID:            blockID,
		AppHash:            appHash,
	}

	vpb := v.ToProto()

	if err := val.SignVote(ctx, chainID, valSet.QuorumType, valSet.QuorumHash, vpb, v.StateID(), nil); err != nil {
		return nil, err
	}

	v.BlockSignature = vpb.BlockSignature
	v.StateSignature = vpb.StateSignature
	err = v.VoteExtensions.CopySignsFromProto(vpb.VoteExtensionsToMap())
	if err != nil {
		return nil, err
	}
	return v, nil
}
