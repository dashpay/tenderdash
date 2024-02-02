package types

import (
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/proto/tendermint/types"
)

func TestBlockRequestID(t *testing.T) {
	expected := tmbytes.MustHexDecode("28277743e77872951df01bda93a344feca2435e113b8824ce636eada665aadd5")
	got := BlockRequestID(12, 34)
	assert.EqualValues(t, expected, got)
}

func TestMakeBlockSignItem(t *testing.T) {
	const chainID = "dash-platform"
	const quorumType = btcjson.LLMQType_5_60

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

func newSignItem(reqID, signHash, raw, quorumHash string, quorumType btcjson.LLMQType) SignItem {
	item := NewSignItem(quorumType, tmbytes.MustHexDecode(quorumHash), tmbytes.MustHexDecode(reqID), tmbytes.MustHexDecode(raw))
	item.SignHash = tmbytes.MustHexDecode(signHash)
	return item
}

func TestQuorumSignItem(t *testing.T) {

	si := SignItem{
		ID:         mustHexDecode("87cda9461081793e7e31ab1def8ffbd453775a0f9987304598398d42a78d68d4"),
		MsgHash:    mustHexDecode("5ef9b9eecc4df7c5aee677c0a72816f4515999a539003cf4bbb6c15c39634c31"),
		LlmqType:   106,
		QuorumHash: mustHexDecode("366f07c9b80a2661563a33c09f02156720159b911186b4438ff281e537674771"),
	}
	si.UpdateSignHash(true)

	expectID := tmbytes.Reverse(mustHexDecode("94635358f4c75a1d0b38314619d1c5d9a16f12961b5314d857e04f2eb61d78d2"))

	assert.EqualValues(t, expectID, si.SignHash)
}

func mustHexDecode(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}
