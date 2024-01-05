package types

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/proto/tendermint/types"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVoteExtensionCopySignsFromProto(t *testing.T) {
	src := tmproto.VoteExtensions{
		&tmproto.VoteExtension{
			Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
			Extension: []byte("threshold"),
			Signature: []byte("signature"),
		},
	}

	dst := VoteExtensions{&ThresholdVoteExtension{}}
	err := dst.CopySignsFromProto(src)
	require.NoError(t, err)
	assert.EqualValues(t, src[0].GetSignature(), dst[0].GetSignature())
}

func TestMakeVoteExtensionsSignItems(t *testing.T) {
	const chainID = "dash-platform"
	const quorumType = btcjson.LLMQType_5_60

	logger := log.NewTestingLogger(t)
	testCases := []struct {
		vote       Vote
		quorumHash []byte
		want       []crypto.SignItem
		wantHash   [][]byte
	}{
		{
			vote: Vote{
				Type:               types.PrecommitType,
				Height:             1001,
				ValidatorProTxHash: tmbytes.MustHexDecode("9CC13F685BC3EA0FCA99B87F42ABCC934C6305AA47F62A32266A2B9D55306B7B"),
				VoteExtensions: VoteExtensionsFromProto(&tmproto.VoteExtension{
					Type:      tmproto.VoteExtensionType_DEFAULT,
					Extension: []byte("default")},
					&tmproto.VoteExtension{
						Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
						Extension: []byte("threshold")},
				),
			},
			quorumHash: tmbytes.MustHexDecode("6A12D9CF7091D69072E254B297AEF15997093E480FDE295E09A7DE73B31CEEDD"),
			want: []crypto.SignItem{
				newSignItem(
					"FB95F2CA6530F02AC623589D7938643FF22AE79A75DD79AEA1C8871162DE675E",
					"533524404D3A905F5AC9A30FCEB5A922EAD96F30DA02F979EE41C4342F540467",
					"210A0764656661756C7411E903000000000000220D646173682D706C6174666F726D",
					"6A12D9CF7091D69072E254B297AEF15997093E480FDE295E09A7DE73B31CEEDD",
					quorumType,
				),
				newSignItem(
					"fb95f2ca6530f02ac623589d7938643ff22ae79a75dd79aea1c8871162de675e",
					"D3B7D53A0F9CA8072D47D6C18E782EE3155EF8DCDDB010087030B6CBC63978BC",
					"250a097468726573686f6c6411e903000000000000220d646173682d706c6174666f726d2801",
					"6A12D9CF7091D69072E254B297AEF15997093E480FDE295E09A7DE73B31CEEDD",
					quorumType,
				),
			},
			wantHash: [][]byte{
				tmbytes.MustHexDecode("61519D79DE4C4D5AC5DD210C1BCE81AA24F76DD5581A24970E60112890C68FB7"),
				tmbytes.MustHexDecode("46C72C423B74034E1AF574A99091B017C0698FEAA55C8B188BFD512FCADD3143"),
			},
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("test-case #%d", i), func(t *testing.T) {
			signItems, err := tc.vote.VoteExtensions.SignItems(chainID, quorumType, tc.quorumHash, tc.vote.Height, tc.vote.Round)

			require.NoError(t, err)

			for i, sign := range signItems {
				assert.Equal(t, tc.wantHash[i], sign.MsgHash, "want %X, actual %X", tc.wantHash[i], sign.MsgHash)
				if !assert.Equal(t, tc.want[i], sign, "Got ID(%d): %X", i, sign.SignHash) {
					logger.Error("invalid sign", "sign", sign, "i", i)
				}
			}
		})
	}
}

// Test vectors of THRESHOLD_RECOVER_RAW vote extensions hashing, as used by Dash Platform withdrawals mechanism.
//
// Given some vote extension, llmq type, quorum hash and sign request id, sign data should match predefined test vector.
func TestVoteExtensionsRaw_SignDataRawVector_Withdrawals(t *testing.T) {
	const chainID = "some-chain" // unused but required for VoteExtension.SignItem
	const llmqType = btcjson.LLMQType_TEST_PLATFORM

	testCases := []struct {
		llmqType   btcjson.LLMQType
		quorumHash []byte
		requestID  []byte
		extension  []byte

		// optional
		quorumSig    []byte
		quorumPubKey []byte

		expectedMsgHash   []byte
		expectedSignHash  []byte
		expectedRequestID []byte
	}{
		{ // llmqType:106
			// quorumHash:53c006055af6d0ae9aa9627df8615a71c312421a28c4712c8add83c8e1bfdadd
			// requestID:922a8fc39b6e265ca761eaaf863387a5e2019f4795a42260805f5562699fd9fa
			// messageHash:2a3b788b83a8a3877d618874c0987ce62b43762ea18362cd336f4a79402d25c0
			// ==== signHash:9753911839e0a8304626b95ada276b55a3785bca657294a153bd5d66301756b7
			llmqType: btcjson.LLMQType_TEST_PLATFORM,

			quorumHash:        tmbytes.MustHexDecode("53c006055af6d0ae9aa9627df8615a71c312421a28c4712c8add83c8e1bfdadd"),
			requestID:         binary.LittleEndian.AppendUint64([]byte("\x06plwdtx"), 0),
			extension:         []byte{192, 37, 45, 64, 121, 74, 111, 51, 205, 98, 131, 161, 46, 118, 67, 43, 230, 124, 152, 192, 116, 136, 97, 125, 135, 163, 168, 131, 139, 120, 59, 42},
			expectedSignHash:  tmbytes.Reverse(tmbytes.MustHexDecode("9753911839e0a8304626b95ada276b55a3785bca657294a153bd5d66301756b7")),
			expectedRequestID: tmbytes.MustHexDecode("922a8fc39b6e265ca761eaaf863387a5e2019f4795a42260805f5562699fd9fa"),
		},
		{ // test that requestID is correct for index 102
			quorumHash:        tmbytes.MustHexDecode("53c006055af6d0ae9aa9627df8615a71c312421a28c4712c8add83c8e1bfdadd"),
			requestID:         binary.LittleEndian.AppendUint64([]byte("\x06plwdtx"), 102),
			extension:         []byte{192, 37, 45, 64, 121, 74, 111, 51, 205, 98, 131, 161, 46, 118, 67, 43, 230, 124, 152, 192, 116, 136, 97, 125, 135, 163, 168, 131, 139, 120, 59, 42},
			expectedRequestID: tmbytes.MustHexDecode("7a1b17a4542f4748c6b91bd46c7daa4f26f77f67cd2d9d405c8d956c77a44764"),
		},
		{
			// tx = 03000900000190cff4050000000023210375aae0756e8115ea064b46705c7b0a8ffad3d79688d910ef0337239fc
			//		1b3760dac000000009101650000000000000070110100db04000031707c372e1a75dab8455659a2c7757842aa80
			// 		998eae146847f1372447a5d02585fe5c9e8f7985ac27d41b1ca654e783bd7eaab484882ceae3cb3511acefac0e8
			//		875b691813ec26101c3384a6e506be9133ba977ae12be89ffa1a1105968fc01c7de4e02ac8c689989a5677ceb2e
			//		284a57d57f7885c40658096d7c6294f9fa7a

			llmqType:   btcjson.LLMQType_TEST,
			quorumHash: tmbytes.MustHexDecode("25d0a5472437f1476814ae8e9980aa427875c7a2595645b8da751a2e377c7031"),
			requestID:  binary.LittleEndian.AppendUint64([]byte("\x06plwdtx"), 101),
			extension:  tmbytes.MustHexDecode("68CA7F464880F4040AF87DBE79F725C74398C3A2001700D7A0FEDB9417FD622F"),

			// TODO: Uncomment when  we have public key
			// quorumSig:    tmbytes.MustHexDecode("85fe5c9e8f7985ac27d41b1ca654e783bd7eaab484882ceae3cb3511acefac0e8875b691813ec26101c3384a6e506be9133ba977ae12be89ffa1a1105968fc01c7de4e02ac8c689989a5677ceb2e284a57d57f7885c40658096d7c6294f9fa7a"),
			// quorumPubKey: tmbytes.MustHexDecode("210375aae0756e8115ea064b46705c7b0a8ffad3d79688d910ef0337239fc1b3760dac"),

			// expectedSignHash:  tmbytes.Reverse(tmbytes.MustHexDecode("9753911839e0a8304626b95ada276b55a3785bca657294a153bd5d66301756b7")),
			expectedMsgHash:   tmbytes.Reverse(tmbytes.MustHexDecode("68ca7f464880f4040af87dbe79f725c74398c3a2001700d7a0fedb9417fd622f")),
			expectedRequestID: tmbytes.MustHexDecode("fcc76a643c5c668244fdcef09833955d6f4b803fa6c459f7732983c2332389fd"),
		},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {

			ve := tmproto.VoteExtension{
				Extension: tc.extension,
				Signature: []byte{},
				Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW,
				XSignRequestId: &tmproto.VoteExtension_SignRequestId{
					SignRequestId: bytes.Clone(tc.requestID),
				},
			}
			voteExtension := VoteExtensionFromProto(ve)
			signItem, err := voteExtension.SignItem(chainID, 1, 0, llmqType, tc.quorumHash)
			require.NoError(t, err)

			// t.Logf("LLMQ type: %s (%d)\n", llmqType.Name(), llmqType)
			// t.Logf("extension: %X\n", extension)
			// t.Logf("sign requestID: %X\n", requestID)
			// t.Logf("quorum hash: %X\n", quorumHash)

			t.Logf("RESULT: sign hash: %X", signItem.SignHash)
			if len(tc.expectedSignHash) > 0 {
				assert.EqualValues(t, tc.expectedSignHash, signItem.SignHash, "sign hash mismatch")
			}
			if len(tc.expectedRequestID) > 0 {
				t.Logf("requestID: %s", hex.EncodeToString(tc.requestID))
				assert.EqualValues(t, tc.expectedRequestID, signItem.ID, "sign request id mismatch")
			}
			if len(tc.expectedMsgHash) > 0 {
				assert.EqualValues(t, tc.expectedMsgHash, signItem.MsgHash, "msg hash mismatch")
			}

			if len(tc.quorumSig) > 0 {
				require.Len(t, tc.quorumPubKey, bls12381.PubKeySize)
				pubKey := bls12381.PubKey(tc.quorumPubKey)
				require.NoError(t, pubKey.Validate(), "invalid public key")
				assert.True(t, pubKey.VerifySignatureDigest(signItem.SignHash, tc.quorumSig), "signature verification failed")
			}
		})
	}

}
