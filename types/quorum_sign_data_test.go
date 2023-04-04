package types

import (
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/proto/tendermint/types"
)

func TestMakeBlockSignID(t *testing.T) {
	const chainID = "dash-platform"
	testCases := []struct {
		vote       Vote
		quorumHash []byte
		want       SignItem
		wantHash   []byte
	}{
		{
			vote: Vote{
				Type:               types.PrecommitType,
				Height:             1001,
				ValidatorProTxHash: mustHexDecode("9CC13F685BC3EA0FCA99B87F42ABCC934C6305AA47F62A32266A2B9D55306B7B"),
			},
			quorumHash: mustHexDecode("6A12D9CF7091D69072E254B297AEF15997093E480FDE295E09A7DE73B31CEEDD"),
			want: newSignItem(
				"C8F2E1FE35DE03AC94F76191F59CAD1BA1F7A3C63742B7125990D996315001CC",
				"DA25B746781DDF47B5D736F30B1D9D0CC86981EEC67CBE255265C4361DEF8C2E",
				"02000000E9030000000000000000000000000000E3B0C44298FC1C149AFBF4C8996FB92427AE41E4649B934CA495991B"+
					"7852B855E3B0C44298FC1C149AFBF4C8996FB92427AE41E4649B934CA495991B7852B855646173682D706C6174666F726D",
			),
			wantHash: mustHexDecode("0CA3D5F42BDFED0C4FDE7E6DE0F046CC76CDA6CEE734D65E8B2EE0E375D4C57D"),
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("test-case %d", i), func(t *testing.T) {
			signItem := MakeBlockSignItem(chainID, tc.vote.ToProto(), btcjson.LLMQType_5_60, tc.quorumHash)
			t.Logf("hash %X id %X raw %X reqID %X", signItem.Hash, signItem.ID, signItem.Raw, signItem.ReqID)
			require.Equal(t, tc.want, signItem)
			require.Equal(t, tc.wantHash, signItem.Hash)
		})
	}
}

func TestMakeVoteExtensionSignsData(t *testing.T) {
	const chainID = "dash-platform"
	logger := log.NewTestingLogger(t)
	testCases := []struct {
		vote       Vote
		quorumHash []byte
		want       map[types.VoteExtensionType][]SignItem
		wantHash   map[types.VoteExtensionType][][]byte
	}{
		{
			vote: Vote{
				Type:               types.PrecommitType,
				Height:             1001,
				ValidatorProTxHash: mustHexDecode("9CC13F685BC3EA0FCA99B87F42ABCC934C6305AA47F62A32266A2B9D55306B7B"),
				VoteExtensions: VoteExtensions{
					types.VoteExtensionType_DEFAULT:           []VoteExtension{{Extension: []byte("default")}},
					types.VoteExtensionType_THRESHOLD_RECOVER: []VoteExtension{{Extension: []byte("threshold")}},
				},
			},
			quorumHash: mustHexDecode("6A12D9CF7091D69072E254B297AEF15997093E480FDE295E09A7DE73B31CEEDD"),
			want: map[types.VoteExtensionType][]SignItem{
				types.VoteExtensionType_DEFAULT: {
					newSignItem(
						"FB95F2CA6530F02AC623589D7938643FF22AE79A75DD79AEA1C8871162DE675E",
						"533524404D3A905F5AC9A30FCEB5A922EAD96F30DA02F979EE41C4342F540467",
						"210A0764656661756C7411E903000000000000220D646173682D706C6174666F726D",
					),
				},
				types.VoteExtensionType_THRESHOLD_RECOVER: {
					newSignItem(
						"fb95f2ca6530f02ac623589d7938643ff22ae79a75dd79aea1c8871162de675e",
						"d3b7d53a0f9ca8072d47d6c18e782ee3155ef8dcddb010087030b6cbc63978bc",
						"250a097468726573686f6c6411e903000000000000220d646173682d706c6174666f726d2801",
					),
				},
			},
			wantHash: map[types.VoteExtensionType][][]byte{
				types.VoteExtensionType_DEFAULT: {
					mustHexDecode("61519D79DE4C4D5AC5DD210C1BCE81AA24F76DD5581A24970E60112890C68FB7"),
				},
				types.VoteExtensionType_THRESHOLD_RECOVER: {
					mustHexDecode("46c72c423b74034e1af574a99091b017c0698feaa55c8b188bfd512fcadd3143"),
				},
			},
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("test-case #%d", i), func(t *testing.T) {
			signItems, err := MakeVoteExtensionSignItems(chainID, tc.vote.ToProto(), btcjson.LLMQType_5_60, tc.quorumHash)
			require.NoError(t, err)
			for et, signs := range signItems {
				for i, sign := range signs {
					assert.Equal(t, tc.wantHash[et][i], sign.Hash, hex.EncodeToString(sign.Hash))
					if !assert.Equal(t, tc.want[et][i], sign) {
						logger.Error("invalid sign", "sign", sign, "type", et, "i", i)
					}
				}
			}
		})
	}
}

func mustHexDecode(b string) []byte {
	r, err := hex.DecodeString(b)
	if err != nil {
		panic(err)
	}
	return r
}

func newSignItem(reqID, ID, raw string) SignItem {
	item := SignItem{
		ReqID: mustHexDecode(reqID),
		ID:    mustHexDecode(ID),
		Raw:   mustHexDecode(raw),
	}
	item.Hash = crypto.Checksum(item.Raw)
	return item
}
