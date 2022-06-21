package types

import (
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/dashevo/dashd-go/btcjson"
	"github.com/stretchr/testify/require"

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
			want: SignItem{
				ReqID: mustHexDecode("C8F2E1FE35DE03AC94F76191F59CAD1BA1F7A3C63742B7125990D996315001CC"),
				ID:    mustHexDecode("CE3AA8C6C6E32F54430C703F198E7E810DFBC7680EBCB549D61B9EBE49530339"),
				Raw:   mustHexDecode("1A080211E903000000000000320D646173682D706C6174666F726D"),
			},
			wantHash: mustHexDecode("4BEAC39C516BEB1FDEBC569C0468B91D999050CA47B4AA12AFA825CD4E7EDAB3"),
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("test-case #%d", i), func(t *testing.T) {
			signItem := MakeBlockSignItem(chainID, tc.vote.ToProto(), btcjson.LLMQType_5_60, tc.quorumHash)
			require.Equal(t, tc.want, signItem)
			require.Equal(t, tc.wantHash, signItem.Hash())
		})
	}
}

func TestMakeStateSignID(t *testing.T) {
	const chainID = "dash-platform"
	testCases := []struct {
		stateID    StateID
		quorumHash []byte
		want       SignItem
		wantHash   []byte
	}{
		{
			stateID: StateID{
				Height:      1001,
				LastAppHash: mustHexDecode("524F1D03D1D81E94A099042736D40BD9681B867321443FF58A4568E274DBD83B"),
			},
			quorumHash: mustHexDecode("6A12D9CF7091D69072E254B297AEF15997093E480FDE295E09A7DE73B31CEEDD"),
			want: SignItem{
				ReqID: mustHexDecode("76D44F9A90D4B7974B3F6CA1A36D203F5163BCDE4A62095E5A0BF65AC94C35C0"),
				ID:    mustHexDecode("8DE1C69FE4F9E89E7BAB5329CF97BD109ECD4E2D04F0B1B41653B1F02A765BA8"),
				Raw:   mustHexDecode("E903000000000000524F1D03D1D81E94A099042736D40BD9681B867321443FF58A4568E274DBD83B"),
			},
			wantHash: mustHexDecode("85944D1C7755EDCDA86815CC69CF3961E5AAC5F6CB214B256EA5907195603ED4"),
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("test-case #%d", i), func(t *testing.T) {
			signItem := MakeStateSignItem(chainID, tc.stateID, btcjson.LLMQType_5_60, tc.quorumHash)
			require.Equal(t, tc.want, signItem)
		})
	}
}

func TestMakeVoteExtensionSignsData(t *testing.T) {
	const chainID = "dash-platform"
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
					{
						ReqID: mustHexDecode("FB95F2CA6530F02AC623589D7938643FF22AE79A75DD79AEA1C8871162DE675E"),
						ID:    mustHexDecode("533524404D3A905F5AC9A30FCEB5A922EAD96F30DA02F979EE41C4342F540467"),
						Raw:   mustHexDecode("210A0764656661756C7411E903000000000000220D646173682D706C6174666F726D"),
					},
				},
				types.VoteExtensionType_THRESHOLD_RECOVER: {
					{
						ReqID: mustHexDecode("FB95F2CA6530F02AC623589D7938643FF22AE79A75DD79AEA1C8871162DE675E"),
						ID:    mustHexDecode("32EEC36505B3E47E97C210B4BC386538128688B1575EC428904270A131D43EBD"),
						Raw:   mustHexDecode("260A097468726573686F6C6411E903000000000000220D646173682D706C6174666F726DA00601"),
					},
				},
			},
			wantHash: map[types.VoteExtensionType][][]byte{
				types.VoteExtensionType_DEFAULT: {
					mustHexDecode("61519D79DE4C4D5AC5DD210C1BCE81AA24F76DD5581A24970E60112890C68FB7"),
				},
				types.VoteExtensionType_THRESHOLD_RECOVER: {
					mustHexDecode("E5E9DE2371FC580F944CAA0725A876B8490ADF4FD430ED38743054E3AC189EEC"),
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
					require.Equal(t, tc.wantHash[et][i], sign.Hash())
					require.Equal(t, tc.want[et][i], sign)
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
