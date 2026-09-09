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
	pubKey, err := encoding.PubKeyFromProto(vsu.ThresholdPublicKey)
	if err != nil {
		return err
	}

	extSigs := make([][]byte, 0, len(commit.ThresholdVoteExtensions))
	for _, ext := range commit.ThresholdVoteExtensions {
		extSigs = append(extSigs, ext.Signature)
	}

	return qsd.Verify(pubKey, types.QuorumSigns{
		BlockSign:               commit.BlockSignature,
		VoteExtensionSignatures: extSigs,
	})
}

func makeBlockSignItem(
	req *abci.RequestFinalizeBlock,
	quorumType btcjson.LLMQType,
	quorumHash []byte,
) (types.SignItem, error) {
	reqID := types.BlockRequestID(req.Height, req.Round)
	cv, err := req.ToCanonicalVote()
	if err != nil {
		return types.SignItem{}, fmt.Errorf("block sign item: %w", err)
	}
	raw, err := tmbytes.MarshalFixedSize(cv)
	if err != nil {
		return types.SignItem{}, fmt.Errorf("block sign item: %w", err)
	}
	return types.NewSignItem(quorumType, quorumHash, reqID, raw), nil
}

func makeVoteExtensionSignItems(
	req *abci.RequestFinalizeBlock,
	quorumType btcjson.LLMQType,
	quorumHash []byte,
) ([]types.SignItem, error) {

	extensions, err := types.VoteExtensionsFromProto(req.Commit.ThresholdVoteExtensions...)
	if err != nil {
		return nil, fmt.Errorf("vote extensions from proto: %w", err)
	}
	chainID := req.Block.Header.ChainID

	items, err := extensions.SignItems(chainID, quorumType, quorumHash, req.Height, req.Round)
	if err != nil {
		return nil, fmt.Errorf("vote extension sign items: %w", err)
	}
	return items, nil
}
