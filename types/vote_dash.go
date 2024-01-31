package types

import tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"

// PopulateSignsFromProto updates the signatures of the current Vote with values are taken from the Vote's protobuf
func (vote *Vote) PopulateSignsFromProto(pv *tmproto.Vote) error {
	vote.BlockSignature = pv.BlockSignature
	return vote.VoteExtensions.CopySignsFromProto(pv.VoteExtensions)
}

// PopulateSignsToProto updates the signatures of the given protobuf Vote entity with values are taken from the current Vote's
func (vote *Vote) PopulateSignsToProto(pv *tmproto.Vote) error {
	pv.BlockSignature = vote.BlockSignature
	return vote.VoteExtensions.CopySignsToProto(pv.VoteExtensions)
}
