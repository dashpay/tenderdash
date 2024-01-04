package types

import (
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/proto/tendermint/types"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

func TestBlockRequestID(t *testing.T) {
	expected := tmbytes.MustHexDecode("28277743e77872951df01bda93a344feca2435e113b8824ce636eada665aadd5")
	got := BlockRequestID(12, 34)
	assert.EqualValues(t, expected, got)
}

func TestMakeBlockSignID(t *testing.T) {
	const chainID = "dash-platform"
	const quorumType = btcjson.LLMQType_5_60

	testCases := []struct {
		vote       Vote
		quorumHash []byte
		want       crypto.SignItem
		wantHash   []byte
	}{
		{
			vote: Vote{
				Type:               types.PrecommitType,
				Height:             1001,
				ValidatorProTxHash: tmbytes.MustHexDecode("9CC13F685BC3EA0FCA99B87F42ABCC934C6305AA47F62A32266A2B9D55306B7B"),
			},
			quorumHash: tmbytes.MustHexDecode("6A12D9CF7091D69072E254B297AEF15997093E480FDE295E09A7DE73B31CEEDD"),
			want: newSignItem(
				"C8F2E1FE35DE03AC94F76191F59CAD1BA1F7A3C63742B7125990D996315001CC",
				"DA25B746781DDF47B5D736F30B1D9D0CC86981EEC67CBE255265C4361DEF8C2E",
				"02000000E9030000000000000000000000000000E3B0C44298FC1C149AFBF4C8996FB92427AE41E4649B934CA495991B"+
					"7852B855E3B0C44298FC1C149AFBF4C8996FB92427AE41E4649B934CA495991B7852B855646173682D706C6174666F726D",
				"6A12D9CF7091D69072E254B297AEF15997093E480FDE295E09A7DE73B31CEEDD",
				quorumType,
			),
			wantHash: tmbytes.MustHexDecode("0CA3D5F42BDFED0C4FDE7E6DE0F046CC76CDA6CEE734D65E8B2EE0E375D4C57D"),
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("test-case %d", i), func(t *testing.T) {
			signItem := MakeBlockSignItem(chainID, tc.vote.ToProto(), quorumType, tc.quorumHash)
			t.Logf("hash %X id %X raw %X reqID %X", signItem.MsgHash, signItem.SignHash, signItem.Msg, signItem.ID)
			require.Equal(t, tc.want, signItem, "Got ID: %X", signItem.SignHash)
			require.Equal(t, tc.wantHash, signItem.MsgHash)
		})
	}
}

func TestMakeVoteExtensionSignsData(t *testing.T) {
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

// TestVoteExtensionsRawSignData checks signed data for a VoteExtensionType_THRESHOLD_RECOVER_RAW vote extension type.
//
// Given some vote extension, llmq type, quorum hash and sign request id, sign data should match predefined test vector.
func TestVoteExtensionsRawSignDataRawVector(t *testing.T) {
	// t.Skip("TODO: this should use the same test vectors as dash core")
	const chainID = "some-chain"
	const llmqType = btcjson.LLMQType_TEST_PLATFORM

	// threshold_vote_extensions: [VoteExtension { r#type: ThresholdRecoverRaw,
	//  signature: [172, 4, 13, 160, 146, 213, 36, 188, 135, 233, 210, 83, 219, 136, 137, 56, 239, 214, 169, 192, 230, 76, 12, 129, 225, 221, 226, 53, 121, 4, 208, 206, 187, 5, 189, 177, 153, 248, 113, 211, 48, 64, 106, 119, 168, 92, 231, 199, 12, 225, 210, 227, 198, 53, 89, 178, 166, 144, 35, 58, 74, 230, 154, 60, 86, 193, 197, 113, 24, 92, 119, 211, 31, 50, 175, 139, 63, 182, 1, 236, 181, 55, 183, 240, 70, 101, 152, 134, 17, 45, 17, 107, 81, 112, 101, 3],
	//   sign_request_id: Some([6, 112, 108, 119, 100, 116, 120, 0, 0, 0, 0, 0, 0, 0, 0]) }] }

	// TODO
	// quorumHash, err := hex.DecodeString("dddabfe1c883dd8a2c71c4281a4212c3715a61f87d62a99aaed0f65a0506c053")
	// assert.NoError(t, err)
	// assert.Len(t, quorumHash, 32)

	// requestID, err := hex.DecodeString("922a8fc39b6e265ca761eaaf863387a5e2019f4795a42260805f5562699fd9fa")
	// assert.NoError(t, err)
	// assert.Len(t, requestID, 32)

	// extension, err := hex.DecodeString("7dfb2432d37f004c4eb2b9aebf601ba4ad59889b81d2e8c7029dce3e0bf8381c")
	// assert.NoError(t, err)
	// assert.Len(t, extension, 32)
	quorumHash, err := hex.DecodeString("1cdbea29c8b947d6a35269aabbd0cccfef64245778a62116524ccdbb6e3ec919")
	assert.NoError(t, err)
	assert.Len(t, quorumHash, 32)

	assert.NoError(t, err)

	extension := []byte{209, 226, 137, 236, 75, 102, 97, 95, 224, 61, 203, 233, 87, 62, 153, 90, 177, 26, 169, 127, 239, 22, 141, 238, 175, 198, 120, 8, 156, 150, 182, 166}
	assert.Len(t, extension, 32)

	requestID := []byte{6, 112, 108, 119, 100, 116, 120, 0, 0, 0, 0, 0, 0, 0, 0}
	signature := []byte{172, 4, 13, 160, 146, 213, 36, 188, 135, 233, 210, 83, 219, 136, 137, 56, 239, 214, 169, 192, 230, 76, 12, 129, 225, 221, 226, 53, 121, 4, 208, 206, 187, 5, 189, 177, 153, 248, 113, 211, 48, 64, 106, 119, 168, 92, 231, 199, 12, 225, 210, 227, 198, 53, 89, 178, 166, 144, 35, 58, 74, 230, 154, 60, 86, 193, 197, 113, 24, 92, 119, 211, 31, 50, 175, 139, 63, 182, 1, 236, 181, 55, 183, 240, 70, 101, 152, 134, 17, 45, 17, 107, 81, 112, 101, 3}

	expected, err := hex.DecodeString("B7561730665DBD53A1947265CA5B78A3556B27DA5AB9264630A8E03918915397")
	assert.NoError(t, err)
	assert.Len(t, expected, 32)
	// expected = tmbytes.Reverse(expected)

	// Note: MakeVoteExtensionSignItems() calls MakeSignID(), which will reverse bytes in quorumHash, requestID and extension.

	ve := VoteExtensionFromProto(tmproto.VoteExtension{
		Extension: extension,
		Signature: signature,
		Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW,
		XSignRequestId: &tmproto.VoteExtension_SignRequestId{
			SignRequestId: requestID,
		},
	})

	item, err := ve.SignItem(chainID, 1, 0, llmqType, quorumHash)
	assert.NoError(t, err)
	actual := item.SignHash

	t.Logf("LLMQ type: %s (%d)\n", llmqType.Name(), llmqType)
	t.Logf("extension: %X\n", extension)
	t.Logf("sign requestID: %X\n", requestID)
	t.Logf("quorum hash: %X\n", quorumHash)

	t.Logf("RESULT: signHash: %X", actual)
	assert.EqualValues(t, expected, actual)

}

func newSignItem(reqID, signHash, raw, quorumHash string, quorumType btcjson.LLMQType) crypto.SignItem {
	item := crypto.NewSignItem(quorumType, tmbytes.MustHexDecode(quorumHash), tmbytes.MustHexDecode(reqID), tmbytes.MustHexDecode(raw))
	item.SignHash = tmbytes.MustHexDecode(signHash)
	return item
}
