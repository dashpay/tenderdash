package kvstore

import (
	"bytes"
	"fmt"

	"github.com/dashpay/dashd-go/btcjson"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto/encoding"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
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

	extSigs := make([][]byte, 0, len(commit.ThresholdVoteExtensions))
	for _, ext := range commit.ThresholdVoteExtensions {
		extSigs = append(extSigs, ext.Signature)
	}

	return verifier.Verify(pubKey, types.QuorumSigns{
		BlockSign:               commit.BlockSignature,
		VoteExtensionSignatures: extSigs,
	})
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
) []types.SignItem {

	extensions := types.VoteExtensionsFromProto(req.Commit.ThresholdVoteExtensions...)
	chainID := req.Block.Header.ChainID

	items, err := extensions.SignItems(chainID, quorumType, quorumHash, req.Height, req.Round)
	if err != nil {
		panic(fmt.Errorf("vote extension sign items: %w", err))
	}
	return items
}
