package types

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/dashpay/dashd-go/btcjson"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/proto/tendermint/types"
)

var (
	errUnexpectedVoteType = errors.New("unexpected vote extension - vote extensions are only allowed in precommits")
)

// QuorumSignData holds data which is necessary for signing and verification block, state, and each vote-extension in a list
type QuorumSignData struct {
	Block      crypto.SignItem
	Extensions map[types.VoteExtensionType][]crypto.SignItem
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
	quorumSign.Extensions, err = MakeVoteExtensionSignItems(chainID, protoVote, quorumType, quorumHash)
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

// MakeVoteExtensionSignItems  creates a list SignItem structs for a vote extensions
func MakeVoteExtensionSignItems(
	chainID string,
	protoVote *types.Vote,
	quorumType btcjson.LLMQType,
	quorumHash []byte,
) (map[types.VoteExtensionType][]crypto.SignItem, error) {
	// We only sign vote extensions for precommits
	if protoVote.Type != types.PrecommitType {
		if len(protoVote.VoteExtensions) > 0 {
			return nil, errUnexpectedVoteType
		}
		return nil, nil
	}
	items := make(map[types.VoteExtensionType][]crypto.SignItem)
	protoExtensionsMap := protoVote.VoteExtensionsToMap()
	for t, exts := range protoExtensionsMap {
		if items[t] == nil && len(exts) > 0 {
			items[t] = make([]crypto.SignItem, len(exts))
		}

		for i, ext := range exts {
			reqID, err := VoteExtensionRequestID(ext, protoVote.Height, protoVote.Round)
			if err != nil {
				return nil, err
			}
			raw := VoteExtensionSignBytes(chainID, protoVote.Height, protoVote.Round, ext)
			// TODO: this is to avoid sha256 of raw data, to be removed once we get into agreement on the format
			if ext.Type == types.VoteExtensionType_THRESHOLD_RECOVER_RAW {
				// for this vote extension type, we just sign raw data from extension
				msgHash := bytes.Clone(raw)
				items[t][i] = crypto.NewSignItemFromHash(quorumType, quorumHash, reqID, msgHash)
				items[t][i].Raw = raw
			} else {
				items[t][i] = crypto.NewSignItem(quorumType, quorumHash, reqID, raw)
			}
		}
	}
	return items, nil
}
