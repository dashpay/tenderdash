package types

import (
	"context"
	"strings"
	"testing"

	"github.com/dashevo/dashd-go/btcjson"

	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tendermint/tendermint/crypto/bls12381"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/tmhash"
	"github.com/tendermint/tendermint/internal/libs/protoio"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
)

func examplePrevote() *Vote {
	return exampleVote(byte(tmproto.PrevoteType))
}

func examplePrecommit() *Vote {
	return exampleVote(byte(tmproto.PrecommitType))
}

func exampleVote(t byte) *Vote {
	return &Vote{
		Type:   tmproto.SignedMsgType(t),
		Height: 12345,
		Round:  2,
		BlockID: BlockID{
			Hash: tmhash.Sum([]byte("blockID_hash")),
			PartSetHeader: PartSetHeader{
				Total: 1000000,
				Hash:  tmhash.Sum([]byte("blockID_part_set_header_hash")),
			},
		},
		ValidatorProTxHash: crypto.ProTxHashFromSeedBytes([]byte("validator_pro_tx_hash")),
		ValidatorIndex:     56789,
	}
}

func TestVoteSignable(t *testing.T) {
	vote := examplePrecommit()
	v := vote.ToProto()
	signBytes := VoteBlockSignBytes("test_chain_id", v)
	pb := CanonicalizeVote("test_chain_id", v)
	expected, err := protoio.MarshalDelimited(&pb)
	require.NoError(t, err)

	require.Equal(t, expected, signBytes, "Got unexpected sign bytes for Vote.")
}

func TestVoteSignBytesTestVectors(t *testing.T) {

	tests := []struct {
		chainID string
		vote    *Vote
		want    []byte
	}{
		0: {
			"", &Vote{},
			// NOTE: Height and Round are skipped here. This case needs to be considered while parsing.
			[]byte{0x0},
		},
		// with proper (fixed size) height and round (PreCommit):
		1: {
			"", &Vote{Height: 1, Round: 1, Type: tmproto.PrecommitType},
			[]byte{
				0x14,                                   // length
				0x8,                                    // (field_number << 3) | wire_type
				0x2,                                    // PrecommitType
				0x11,                                   // (field_number << 3) | wire_type
				0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, // height
				0x19,                                   // (field_number << 3) | wire_type
				0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, // round
			},
		},
		// with proper (fixed size) height and round (PreVote):
		2: {
			"", &Vote{Height: 1, Round: 1, Type: tmproto.PrevoteType},
			[]byte{
				0x14,                                   // length
				0x8,                                    // (field_number << 3) | wire_type
				0x1,                                    // PrevoteType
				0x11,                                   // (field_number << 3) | wire_type
				0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, // height
				0x19,                                   // (field_number << 3) | wire_type
				0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, // round
			},
		},
		3: {
			"", &Vote{Height: 1, Round: 1},
			[]byte{
				0x12,                                   // length
				0x11,                                   // (field_number << 3) | wire_type
				0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, // height
				0x19,                                   // (field_number << 3) | wire_type
				0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, // round
			},
		},
		// containing non-empty chain_id:
		4: {
			"test_chain_id", &Vote{Height: 1, Round: 1},
			[]byte{
				0x21,                                   // length
				0x11,                                   // (field_number << 3) | wire_type
				0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, // height
				0x19,                                   // (field_number << 3) | wire_type
				0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, // round
				// remaining fields:
				// (field_number << 3) | wire_type
				0x32,
				0xd, 0x74, 0x65, 0x73, 0x74, 0x5f, 0x63, 0x68, 0x61, 0x69, 0x6e, 0x5f, 0x69, 0x64}, // chainID
		},
	}
	for i, tc := range tests {
		v := tc.vote.ToProto()
		got := VoteBlockSignBytes(tc.chainID, v)
		assert.Equal(t, len(tc.want), len(got), "test case #%v: got unexpected sign bytes length for Vote.", i)
		assert.Equal(t, tc.want, got, "test case #%v: got unexpected sign bytes for Vote.", i)
	}
}

func TestVoteStateSignBytesTestVectors(t *testing.T) {
	tests := []struct {
		chainID string
		height  int64
		apphash []byte
		want    []byte
	}{
		0: {
			"", 1, []byte("12345678901234567890123456789012"),
			// NOTE: Height and Round are skipped here. This case needs to be considered while parsing.
			[]byte{0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x31, 0x32, 0x33, 0x34, 0x35, 0x36, 0x37, 0x38,
				0x39, 0x30, 0x31, 0x32, 0x33, 0x34, 0x35, 0x36, 0x37, 0x38, 0x39, 0x30, 0x31, 0x32, 0x33,
				0x34, 0x35, 0x36, 0x37, 0x38, 0x39, 0x30, 0x31, 0x32},
		},
	}
	for i, tc := range tests {
		sid := StateID{
			Height:      tc.height,
			LastAppHash: tc.apphash,
		}
		got := sid.SignBytes(tc.chainID)
		assert.Equal(t, len(tc.want), len(got), "test case #%v: got unexpected sign bytes length for Vote.", i)
		assert.Equal(t, tc.want, got, "test case #%v: got unexpected sign bytes for Vote.", i)
	}
}

func TestVoteProposalNotEq(t *testing.T) {
	cv := CanonicalizeVote("", &tmproto.Vote{Height: 1, Round: 1})
	p := CanonicalizeProposal("", &tmproto.Proposal{Height: 1, Round: 1})
	vb, err := proto.Marshal(&cv)
	require.NoError(t, err)
	pb, err := proto.Marshal(&p)
	require.NoError(t, err)
	require.NotEqual(t, vb, pb)
}

func TestVoteVerifySignature(t *testing.T) {
	quorumHash := crypto.RandQuorumHash()
	privVal := NewMockPVForQuorum(quorumHash)
	pubkey, err := privVal.GetPubKey(context.Background(), quorumHash)
	require.NoError(t, err)

	vote := examplePrecommit()
	v := vote.ToProto()
	stateID := RandStateID().WithHeight(vote.Height - 1)
	quorumType := btcjson.LLMQType_5_60
	signID := VoteBlockSignID("test_chain_id", v, quorumType, quorumHash)
	signStateID := stateID.SignID("test_chain_id", quorumType, quorumHash)

	// sign it
	err = privVal.SignVote(context.Background(), "test_chain_id", quorumType, quorumHash, v, stateID, nil)
	require.NoError(t, err)

	// verify the same vote
	valid := pubkey.VerifySignatureDigest(signID, v.BlockSignature)
	require.True(t, valid)

	// verify the same vote
	valid = pubkey.VerifySignatureDigest(signStateID, v.StateSignature)
	require.True(t, valid)

	// serialize, deserialize and verify again....
	precommit := new(tmproto.Vote)
	bs, err := proto.Marshal(v)
	require.NoError(t, err)
	err = proto.Unmarshal(bs, precommit)
	require.NoError(t, err)

	// verify the transmitted vote
	newSignID := VoteBlockSignID("test_chain_id", precommit, quorumType, quorumHash)
	newSignStateID := stateID.SignID("test_chain_id", quorumType, quorumHash)
	require.Equal(t, string(signID), string(newSignID))
	require.Equal(t, string(signStateID), string(newSignStateID))
	valid = pubkey.VerifySignatureDigest(newSignID, precommit.BlockSignature)
	require.True(t, valid)
	valid = pubkey.VerifySignatureDigest(newSignStateID, precommit.StateSignature)
	require.True(t, valid)
}

func TestIsVoteTypeValid(t *testing.T) {
	tc := []struct {
		name string
		in   tmproto.SignedMsgType
		out  bool
	}{
		{"Prevote", tmproto.PrevoteType, true},
		{"Precommit", tmproto.PrecommitType, true},
		{"InvalidType", tmproto.SignedMsgType(0x3), false},
	}

	for _, tt := range tc {
		tt := tt
		t.Run(tt.name, func(st *testing.T) {
			if rs := IsVoteTypeValid(tt.in); rs != tt.out {
				t.Errorf("got unexpected Vote type. Expected:\n%v\nGot:\n%v", rs, tt.out)
			}
		})
	}
}

func TestVoteVerify(t *testing.T) {
	quorumHash := crypto.RandQuorumHash()
	privVal := NewMockPVForQuorum(quorumHash)
	proTxHash, err := privVal.GetProTxHash(context.Background())
	require.NoError(t, err)

	quorumType := btcjson.LLMQType_5_60

	pubkey, err := privVal.GetPubKey(context.Background(), quorumHash)
	require.NoError(t, err)

	vote := examplePrevote()
	vote.ValidatorProTxHash = proTxHash

	stateID := RandStateID().WithHeight(vote.Height - 1)
	_, _, err = vote.Verify("test_chain_id", quorumType, quorumHash, bls12381.GenPrivKey().PubKey(),
		crypto.RandProTxHash(), stateID)

	if assert.Error(t, err) {
		assert.Equal(t, ErrVoteInvalidValidatorProTxHash, err)
	}

	_, _, err = vote.Verify("test_chain_id", quorumType, quorumHash, pubkey, proTxHash, stateID)
	if assert.Error(t, err) {
		assert.True(
			t, strings.HasPrefix(err.Error(), ErrVoteInvalidBlockSignature.Error()),
		) // since block signatures are verified first
	}
}

func TestVoteString(t *testing.T) {
	str := examplePrecommit().String()
	expected := `Vote{56789:959A8F5EF2BE 12345/02/SIGNED_MSG_TYPE_PRECOMMIT(Precommit) 8B01023386C3 000000000000 000000000000}`
	if str != expected {
		t.Errorf("got unexpected string for Vote. Expected:\n%v\nGot:\n%v", expected, str)
	}

	str2 := examplePrevote().String()
	expected = `Vote{56789:959A8F5EF2BE 12345/02/SIGNED_MSG_TYPE_PREVOTE(Prevote) 8B01023386C3 000000000000 000000000000}`
	if str2 != expected {
		t.Errorf("got unexpected string for Vote. Expected:\n%v\nGot:\n%v", expected, str2)
	}
}

func TestVoteValidateBasic(t *testing.T) {
	quorumHash := crypto.RandQuorumHash()
	privVal := NewMockPVForQuorum(quorumHash)

	testCases := []struct {
		testName     string
		malleateVote func(*Vote)
		expectErr    bool
	}{
		{"Good Vote", func(v *Vote) {}, false},
		{"Negative Height", func(v *Vote) { v.Height = -1 }, true},
		{"Negative Round", func(v *Vote) { v.Round = -1 }, true},
		{"Invalid BlockID", func(v *Vote) {
			v.BlockID = BlockID{[]byte{1, 2, 3}, PartSetHeader{111, []byte("blockparts")}}
		}, true},
		{"Invalid ProTxHash", func(v *Vote) { v.ValidatorProTxHash = make([]byte, 1) }, true},
		{"Invalid ValidatorIndex", func(v *Vote) { v.ValidatorIndex = -1 }, true},
		{"Invalid Signature", func(v *Vote) { v.BlockSignature = nil }, true},
		{"Too big Signature", func(v *Vote) { v.BlockSignature = make([]byte, SignatureSize+1) }, true},
	}
	for _, tc := range testCases {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			vote := examplePrecommit()
			v := vote.ToProto()
			stateID := RandStateID().WithHeight(v.Height - 1)
			err := privVal.SignVote(context.Background(), "test_chain_id", 0, quorumHash, v, stateID, nil)
			vote.BlockSignature = v.BlockSignature
			vote.StateSignature = v.StateSignature
			require.NoError(t, err)
			tc.malleateVote(vote)
			assert.Equal(t, tc.expectErr, vote.ValidateBasic() != nil, "Validate Basic had an unexpected result")
		})
	}
}

func TestVoteProtobuf(t *testing.T) {
	quorumHash := crypto.RandQuorumHash()
	privVal := NewMockPVForQuorum(quorumHash)
	vote := examplePrecommit()
	v := vote.ToProto()
	stateID := RandStateID().WithHeight(v.Height - 1)
	err := privVal.SignVote(context.Background(), "test_chain_id", 0, quorumHash, v, stateID, nil)
	vote.BlockSignature = v.BlockSignature
	vote.StateSignature = v.StateSignature
	require.NoError(t, err)

	testCases := []struct {
		msg     string
		v1      *Vote
		expPass bool
	}{
		{"success", vote, true},
		{"fail vote validate basic", &Vote{}, false},
		{"failure nil", nil, false},
	}
	for _, tc := range testCases {
		protoProposal := tc.v1.ToProto()

		v, err := VoteFromProto(protoProposal)
		if tc.expPass {
			require.NoError(t, err)
			require.Equal(t, tc.v1, v, tc.msg)
		} else {
			require.Error(t, err)
		}
	}
}
