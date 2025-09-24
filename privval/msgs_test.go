package privval

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	"github.com/dashpay/tenderdash/crypto/encoding"
	cryptoproto "github.com/dashpay/tenderdash/proto/tendermint/crypto"
	privproto "github.com/dashpay/tenderdash/proto/tendermint/privval"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

var stamp = time.Date(2019, 10, 13, 16, 14, 44, 0, time.UTC)

func exampleVote() *types.Vote {

	ts := uint64(time.Date(2022, 3, 4, 5, 6, 7, 8, time.UTC).UnixMilli())

	return &types.Vote{
		Type:   tmproto.PrecommitType,
		Height: 3,
		Round:  2,
		BlockID: types.BlockID{
			Hash: crypto.Checksum([]byte("blockID_hash")),
			PartSetHeader: types.PartSetHeader{
				Total: 1000000,
				Hash:  crypto.Checksum([]byte("blockID_part_set_header_hash")),
			},
			StateID: tmproto.StateID{
				AppVersion:            types.StateIDVersion,
				Height:                3,
				AppHash:               crypto.Checksum([]byte("apphash")),
				CoreChainLockedHeight: 12345,
				Time:                  ts,
			}.Hash(),
		},
		ValidatorProTxHash: crypto.ProTxHashFromSeedBytes([]byte("validator_pro_tx_hash")),
		ValidatorIndex:     56789,
		VoteExtensions: types.VoteExtensionsFromProto(&tmproto.VoteExtension{
			Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
			Extension: []byte("extension"),
		}),
	}
}

func exampleProposal() *types.Proposal {

	return &types.Proposal{
		Type:      tmproto.SignedMsgType(1),
		Height:    3,
		Round:     2,
		Timestamp: stamp,
		POLRound:  2,
		Signature: []byte("it's a signature"),
		BlockID: types.BlockID{
			Hash: crypto.Checksum([]byte("blockID_hash")),
			PartSetHeader: types.PartSetHeader{
				Total: 1000000,
				Hash:  crypto.Checksum([]byte("blockID_part_set_header_hash")),
			},
		},
	}
}

func TestPrivvalVectors(t *testing.T) {
	pk := bls12381.GenPrivKeyFromSecret([]byte("it's a secret")).PubKey()
	ppk, err := encoding.PubKeyToProto(pk)
	require.NoError(t, err)

	// Generate a simple vote
	vote := exampleVote()
	votepb := vote.ToProto()

	// Generate a simple proposal
	proposal := exampleProposal()
	proposalpb := proposal.ToProto()

	// Create a Reuseable remote error
	remoteError := &privproto.RemoteSignerError{Code: 1, Description: "it's a error"}

	testCases := []struct {
		testName string
		msg      proto.Message
		expBytes string
	}{
		{"ping request", &privproto.PingRequest{}, "3a00"},
		{"ping response", &privproto.PingResponse{}, "4200"},
		{"pubKey request", &privproto.PubKeyRequest{}, "0a00"},
		{"pubKey response", &privproto.PubKeyResponse{PubKey: ppk, Error: nil}, "12340a321a30991a1c4f159f8e4730bf897e97e27c11f27ba0c1337111a3c102e1081a19372832b596623b1a248a0e00b156d80690cf"},
		{"pubKey response with error", &privproto.PubKeyResponse{PubKey: cryptoproto.PublicKey{}, Error: remoteError}, "12140a0012100801120c697427732061206572726f72"},
		{"Vote Request", &privproto.SignVoteRequest{Vote: votepb}, "1aac010aa901080210031802226c0a208b01023386c371778ecb6368573e539afc3cc860ec3a2f614e54fe5652f4fc80122608c0843d122072db3d959635dff1bb567bedaa70573392c5159666a3f8caf11e413aac52207a1a20b583d49b95a0a5526966b519d8b7bba2aefc800b370a7438c7063728904e58ee2a20959a8f5ef2be68d0ed3a07ed8cff85991ee7995c2ac17030f742c135f9729fbe30d5bb03420d08011209657874656e73696f6e"},
		{"Vote Response", &privproto.SignedVoteResponse{Vote: *votepb, Error: nil}, "22ac010aa901080210031802226c0a208b01023386c371778ecb6368573e539afc3cc860ec3a2f614e54fe5652f4fc80122608c0843d122072db3d959635dff1bb567bedaa70573392c5159666a3f8caf11e413aac52207a1a20b583d49b95a0a5526966b519d8b7bba2aefc800b370a7438c7063728904e58ee2a20959a8f5ef2be68d0ed3a07ed8cff85991ee7995c2ac17030f742c135f9729fbe30d5bb03420d08011209657874656e73696f6e"},
		{"Vote Response with error", &privproto.SignedVoteResponse{Vote: tmproto.Vote{}, Error: remoteError}, "22180a042202120012100801120c697427732061206572726f72"},
		{"Proposal Request", &privproto.SignProposalRequest{Proposal: proposalpb}, "2a700a6e08011003180220022a4a0a208b01023386c371778ecb6368573e539afc3cc860ec3a2f614e54fe5652f4fc80122608c0843d122072db3d959635dff1bb567bedaa70573392c5159666a3f8caf11e413aac52207a320608f49a8ded053a10697427732061207369676e6174757265"},
		{"Proposal Response", &privproto.SignedProposalResponse{Proposal: *proposalpb, Error: nil}, "32700a6e08011003180220022a4a0a208b01023386c371778ecb6368573e539afc3cc860ec3a2f614e54fe5652f4fc80122608c0843d122072db3d959635dff1bb567bedaa70573392c5159666a3f8caf11e413aac52207a320608f49a8ded053a10697427732061207369676e6174757265"},
		{"Proposal Response with error", &privproto.SignedProposalResponse{Proposal: tmproto.Proposal{}, Error: remoteError}, "32250a112a021200320b088092b8c398feffffff0112100801120c697427732061206572726f72"},
	}

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			pm := mustWrapMsg(tc.msg)
			bz, err := pm.Marshal()
			assert.NoError(t, err, tc.testName)
			assert.Equal(t, tc.expBytes, hex.EncodeToString(bz), tc.testName)
		})
	}
}
