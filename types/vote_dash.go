package types

import tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"

// GetVoteExtensionsSigns returns the list of signatures for given vote-extension type
func (vote *Vote) GetVoteExtensionsSigns(extType tmproto.VoteExtensionType) [][]byte {
	if vote.VoteExtensions == nil {
		return nil
	}
	sigs := make([][]byte, len(vote.VoteExtensions[extType]))
	for i, ext := range vote.VoteExtensions[extType] {
		sigs[i] = ext.Signature
	}
	return sigs
}
