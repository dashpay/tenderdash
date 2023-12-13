package kvstore

import (
	"bytes"
	"fmt"

	"github.com/dashpay/dashd-go/btcjson"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto/encoding"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	types1 "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func (app *Application) verifyBlockCommit(qsd types.QuorumSignData, commit abci.CommitInfo) error {
	vsu := app.getActiveValidatorSetUpdates()
	if !bytes.Equal(commit.QuorumHash, vsu.QuorumHash) {
		return fmt.Errorf("mismatch quorum hashes got %X, want %X", commit.QuorumHash, vsu.QuorumHash)
	}
	verifier := types.NewQuorumSignsVerifier(qsd)
	pubKey, err := encoding.PubKeyFromProto(vsu.ThresholdPublicKey)
	if err != nil {
		return err
	}
	return verifier.Verify(pubKey, types.QuorumSigns{
		BlockSign:      commit.BlockSignature,
		ExtensionSigns: makeThresholdVoteExtensions(commit.ThresholdVoteExtensions),
	})
}

func makeThresholdVoteExtensions(pbVoteExtensions []*types1.VoteExtension) []types.ThresholdExtensionSign {
	voteExtensions := types.VoteExtensionsFromProto(pbVoteExtensions)
	var thresholdExtensionSigns []types.ThresholdExtensionSign
	thresholdVoteExtensions, ok := voteExtensions[types1.VoteExtensionType_THRESHOLD_RECOVER]
	if !ok {
		return nil
	}
	thresholdExtensionSigns = make([]types.ThresholdExtensionSign, len(thresholdVoteExtensions))
	for i, voteExtension := range thresholdVoteExtensions {
		thresholdExtensionSigns[i] = types.ThresholdExtensionSign{
			Extension:          voteExtension.Extension,
			ThresholdSignature: voteExtension.Signature,
		}
	}
	return thresholdExtensionSigns
}

func makeBlockSignItem(
	req *abci.RequestFinalizeBlock,
	quorumType btcjson.LLMQType,
	quorumHash []byte,
) types.SignItem {
	reqID := types.BlockRequestID(req.Height, req.Round)
	cv, err := req.ToCanonicalVote()
	if err != nil {
		panic(fmt.Errorf("block sign item: %w", err))
	}
	raw, err := tmbytes.MarshalFixedSize(cv)
	if err != nil {
		panic(fmt.Errorf("block sign item: %w", err))
	}
	return types.NewSignItem(quorumType, quorumHash, reqID, raw)
}

func makeVoteExtensionSignItems(
	req *abci.RequestFinalizeBlock,
	quorumType btcjson.LLMQType,
	quorumHash []byte,
) map[types1.VoteExtensionType][]types.SignItem {
	items := make(map[types1.VoteExtensionType][]types.SignItem)
	protoExtensionsMap := types1.VoteExtensionsToMap(req.Commit.ThresholdVoteExtensions)
	for t, exts := range protoExtensionsMap {
		if items[t] == nil && len(exts) > 0 {
			items[t] = make([]types.SignItem, len(exts))
		}
		chainID := req.Block.Header.ChainID
		for i, ext := range exts {
			raw := types.VoteExtensionSignBytes(chainID, req.Height, req.Round, ext)
			reqID := types.VoteExtensionRequestID(ext, req.Height, req.Round)

			items[t][i] = types.NewSignItem(quorumType, quorumHash, reqID, raw)
		}
	}
	return items
}
