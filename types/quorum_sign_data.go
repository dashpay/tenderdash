package types

import (
	"fmt"

	"github.com/dashpay/dashd-go/btcjson"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/proto/tendermint/types"
)

// QuorumSignData holds data which is necessary for signing and verification block, state, and each vote-extension in a list
type QuorumSignData struct {
	Block                   crypto.SignItem
	ThresholdVoteExtensions []crypto.SignItem
}

// Verify verifies a quorum signatures: block, state and vote-extensions
func (q QuorumSignData) Verify(pubKey crypto.PubKey, signs QuorumSigns) error {
	return NewQuorumSignsVerifier(q).Verify(pubKey, signs)
}

// MakeQuorumSignsWithVoteSet creates and returns QuorumSignData struct built with a vote-set and an added vote
func MakeQuorumSignsWithVoteSet(voteSet *VoteSet, vote *types.Vote) (QuorumSignData, error) {
	return MakeQuorumSigns(
		voteSet.chainID,
		voteSet.valSet.QuorumType,
		voteSet.valSet.QuorumHash,
		vote,
	)
}

// MakeQuorumSigns builds signing data for block, state and vote-extensions
// each a sign-id item consist of request-id, raw data, hash of raw and id
func MakeQuorumSigns(
	chainID string,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	protoVote *types.Vote,
) (QuorumSignData, error) {
	quorumSign := QuorumSignData{
		Block: MakeBlockSignItem(chainID, protoVote, quorumType, quorumHash),
	}
	var err error
	quorumSign.ThresholdVoteExtensions, err =
		VoteExtensionsFromProto(protoVote.VoteExtensions...).
			Filter(func(ext VoteExtensionIf) bool {
				return ext.IsThresholdRecoverable()
			}).
			SignItems(chainID, quorumType, quorumHash, protoVote.Height, protoVote.Round)
	if err != nil {
		return QuorumSignData{}, err
	}
	return quorumSign, nil
}

// MakeBlockSignItem creates SignItem struct for a block
func MakeBlockSignItem(chainID string, vote *types.Vote, quorumType btcjson.LLMQType, quorumHash []byte) crypto.SignItem {
	reqID := BlockRequestID(vote.Height, vote.Round)
	raw, err := vote.SignBytes(chainID)
	if err != nil {
		panic(fmt.Errorf("block sign item: %w", err))
	}
	return crypto.NewSignItem(quorumType, quorumHash, reqID, raw)
}

// BlockRequestID returns a block request ID
func BlockRequestID(height int64, round int32) []byte {
	return heightRoundRequestID("dpbvote", height, round)
}
