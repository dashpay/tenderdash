package types

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	bls "github.com/dashpay/bls-signatures/go-bindings"
	"github.com/dashpay/dashd-go/btcjson"
	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/rand"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

const (
	//nolint: lll
	preCommitTestStr = `Vote{56789:959A8F5EF2BE 12345/02/Precommit(8B01023386C3) 000000000000 03962B14DA9F}`
	//nolint: lll
	preVoteTestStr = `Vote{56789:959A8F5EF2BE 12345/02/Prevote(8B01023386C3) 000000000000 000000000000}`
)

var (
	//nolint: lll
	nilVoteTestStr = fmt.Sprintf(`Vote{56789:959A8F5EF2BE 12345/02/Precommit(%s) 000000000000 000000000000}`, nilVoteStr)
)

func examplePrevote(t *testing.T) *Vote {
	t.Helper()
	return exampleVote(t, byte(tmproto.PrevoteType))
}

func examplePrecommit(t testing.TB) *Vote {
	t.Helper()
	vote := exampleVote(t, byte(tmproto.PrecommitType))
	vote.VoteExtensions = VoteExtensionsFromProto(&tmproto.VoteExtension{
		Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
		Extension: []byte("extension"),
		Signature: make([]byte, SignatureSize),
	})
	return vote
}

func exampleVote(tb testing.TB, t byte) *Vote {
	const (
		height = 12345
		round  = 2
	)
	appHash := bytes.Repeat([]byte{1, 2, 3, 4}, 8)

	tb.Helper()
	stateID := tmproto.StateID{
		Height:                height,
		AppHash:               appHash,
		AppVersion:            StateIDVersion,
		CoreChainLockedHeight: 3,
		Time:                  0,
	}
	return &Vote{
		Type:   tmproto.SignedMsgType(t),
		Height: height,
		Round:  round,
		BlockID: BlockID{
			Hash: crypto.Checksum([]byte("blockID_hash")),
			PartSetHeader: PartSetHeader{
				Total: 1000000,
				Hash:  crypto.Checksum([]byte("blockID_part_set_header_hash")),
			},
			StateID: stateID.Hash(),
		},
		ValidatorProTxHash: crypto.ProTxHashFromSeedBytes([]byte("validator_pro_tx_hash")),
		ValidatorIndex:     56789,
	}
}

func TestVoteSignable(t *testing.T) {
	vote := examplePrecommit(t)
	v := vote.ToProto()
	signBytes, err := v.SignBytes("test_chain_id")
	require.NoError(t, err)
	assert.NotEmpty(t, signBytes)

	hash := crypto.Checksum(signBytes)
	assert.Len(t, hash, crypto.DefaultHashSize)
}

func TestVoteSignBytesTestVectors(t *testing.T) {
	tests := []struct {
		chainID string
		vote    *Vote
		want    []byte
	}{
		0: {
			"", &Vote{},
			[]byte{
				0x0, 0x0, 0x0, 0x0, // Type, 4 bytes
				0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, // Height, 8 bytes
				0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, // Round, 8 bytes

				0xe3, 0xb0, 0xc4, 0x42, 0x98, 0xfc, 0x1c, 0x14, // BlockID, bytes 1-8
				0x9a, 0xfb, 0xf4, 0xc8, 0x99, 0x6f, 0xb9, 0x24, // BlockID, bytes 9-16
				0x27, 0xae, 0x41, 0xe4, 0x64, 0x9b, 0x93, 0x4c, // BlockID, bytes 16-24
				0xa4, 0x95, 0x99, 0x1b, 0x78, 0x52, 0xb8, 0x55, // BlockID, bytes 25-32

				0xe3, 0xb0, 0xc4, 0x42, 0x98, 0xfc, 0x1c, 0x14, // StateID, bytes 1-8
				0x9a, 0xfb, 0xf4, 0xc8, 0x99, 0x6f, 0xb9, 0x24, // StateID, bytes 9-16
				0x27, 0xae, 0x41, 0xe4, 0x64, 0x9b, 0x93, 0x4c, // StateID, bytes 17-24
				0xa4, 0x95, 0x99, 0x1b, 0x78, 0x52, 0xb8, 0x55, // StateID, bytes 25-32
				// empty ChainID
			},
		},
		// with proper (fixed size) height and round (PreCommit):
		1: {
			"", &Vote{Height: 1, Round: 3, Type: tmproto.PrecommitType},
			[]byte{
				0x2, 0x0, 0x0, 0x0, // Type, 4 bytes
				0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, // Height, 8 bytes
				0x3, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, // Round, 8 bytes
				// Block ID, 32 bytes
				0xe3, 0xb0, 0xc4, 0x42, 0x98, 0xfc, 0x1c, 0x14,
				0x9a, 0xfb, 0xf4, 0xc8, 0x99, 0x6f, 0xb9, 0x24,
				0x27, 0xae, 0x41, 0xe4, 0x64, 0x9b, 0x93, 0x4c,
				0xa4, 0x95, 0x99, 0x1b, 0x78, 0x52, 0xb8, 0x55,
				// State ID, 32 bytes
				0xe3, 0xb0, 0xc4, 0x42, 0x98, 0xfc, 0x1c, 0x14,
				0x9a, 0xfb, 0xf4, 0xc8, 0x99, 0x6f, 0xb9, 0x24,
				0x27, 0xae, 0x41, 0xe4, 0x64, 0x9b, 0x93, 0x4c,
				0xa4, 0x95, 0x99, 0x1b, 0x78, 0x52, 0xb8, 0x55,
				// Empty Chain ID
			},
		},
		// with proper (fixed size) height and round (PreVote):
		2: {
			"", &Vote{Height: 1, Round: 1, Type: tmproto.PrevoteType},
			[]byte{
				0x1, 0x0, 0x0, 0x0, // type
				0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, // height
				0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, // round
				// block id
				0xe3, 0xb0, 0xc4, 0x42, 0x98, 0xfc, 0x1c, 0x14,
				0x9a, 0xfb, 0xf4, 0xc8, 0x99, 0x6f, 0xb9, 0x24,
				0x27, 0xae, 0x41, 0xe4, 0x64, 0x9b, 0x93, 0x4c,
				0xa4, 0x95, 0x99, 0x1b, 0x78, 0x52, 0xb8, 0x55,
				// state id
				0xe3, 0xb0, 0xc4, 0x42, 0x98, 0xfc, 0x1c, 0x14,
				0x9a, 0xfb, 0xf4, 0xc8, 0x99, 0x6f, 0xb9, 0x24,
				0x27, 0xae, 0x41, 0xe4, 0x64, 0x9b, 0x93, 0x4c,
				0xa4, 0x95, 0x99, 0x1b, 0x78, 0x52, 0xb8, 0x55,
			},
		},
		3: {
			"", &Vote{Height: 1, Round: 1},
			[]byte{
				0x0, 0x0, 0x0, 0x0, // type
				0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, // height
				0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, //round
				// block id
				0xe3, 0xb0, 0xc4, 0x42, 0x98, 0xfc, 0x1c, 0x14,
				0x9a, 0xfb, 0xf4, 0xc8, 0x99, 0x6f, 0xb9, 0x24,
				0x27, 0xae, 0x41, 0xe4, 0x64, 0x9b, 0x93, 0x4c,
				0xa4, 0x95, 0x99, 0x1b, 0x78, 0x52, 0xb8, 0x55,
				// state id
				0xe3, 0xb0, 0xc4, 0x42, 0x98, 0xfc, 0x1c, 0x14,
				0x9a, 0xfb, 0xf4, 0xc8, 0x99, 0x6f, 0xb9, 0x24,
				0x27, 0xae, 0x41, 0xe4, 0x64, 0x9b, 0x93, 0x4c,
				0xa4, 0x95, 0x99, 0x1b, 0x78, 0x52, 0xb8, 0x55,
			},
		},
		// containing non-empty chain_id:
		4: {
			"test_chain_id", &Vote{Height: 1, Round: 1},
			[]byte{
				0x0, 0x0, 0x0, 0x0, 0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0xe3, 0xb0, 0xc4, 0x42, 0x98, 0xfc, 0x1c, 0x14, 0x9a, 0xfb, 0xf4, 0xc8, 0x99, 0x6f, 0xb9, 0x24, 0x27, 0xae, 0x41, 0xe4, 0x64, 0x9b, 0x93, 0x4c, 0xa4, 0x95, 0x99, 0x1b, 0x78, 0x52, 0xb8, 0x55, 0xe3, 0xb0, 0xc4, 0x42, 0x98, 0xfc, 0x1c, 0x14, 0x9a, 0xfb, 0xf4, 0xc8, 0x99, 0x6f, 0xb9, 0x24, 0x27, 0xae, 0x41, 0xe4, 0x64, 0x9b, 0x93, 0x4c, 0xa4, 0x95, 0x99, 0x1b, 0x78, 0x52, 0xb8, 0x55, 0x74, 0x65, 0x73, 0x74, 0x5f, 0x63, 0x68, 0x61, 0x69, 0x6e, 0x5f, 0x69, 0x64},
		},
		// containing vote extension
		5: {
			"test_chain_id", &Vote{
				Height:         1,
				Round:          1,
				VoteExtensions: VoteExtensionsFromProto(&tmproto.VoteExtension{Extension: []byte("extension")}),
			},
			[]byte{
				0x0, 0x0, 0x0, 0x0, //type
				0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, //height
				0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, //round
				// block id
				0xe3, 0xb0, 0xc4, 0x42, 0x98, 0xfc, 0x1c, 0x14,
				0x9a, 0xfb, 0xf4, 0xc8, 0x99, 0x6f, 0xb9, 0x24,
				0x27, 0xae, 0x41, 0xe4, 0x64, 0x9b, 0x93, 0x4c,
				0xa4, 0x95, 0x99, 0x1b, 0x78, 0x52, 0xb8, 0x55,
				//state id
				0xe3, 0xb0, 0xc4, 0x42, 0x98, 0xfc, 0x1c, 0x14,
				0x9a, 0xfb, 0xf4, 0xc8, 0x99, 0x6f, 0xb9, 0x24,
				0x27, 0xae, 0x41, 0xe4, 0x64, 0x9b, 0x93, 0x4c,
				0xa4, 0x95, 0x99, 0x1b, 0x78, 0x52, 0xb8, 0x55,
				// remaining 13 bytes are chain id
				0x74, 0x65, 0x73, 0x74, 0x5f, 0x63, 0x68, 0x61,
				0x69, 0x6e, 0x5f, 0x69, 0x64,
			},
		},
	}
	for i, tc := range tests {
		t.Run("", func(t *testing.T) {
			v := tc.vote.ToProto()
			got, err := v.SignBytes(tc.chainID)
			assert.NoError(t, err)
			assert.Len(t, got, len(tc.want), "test case #%v: got unexpected sign bytes length for Vote.", i)
			assert.Equal(t, tc.want, got, "test case #%v: got unexpected sign bytes for Vote: %X", i, got)
		})
	}
}

func TestVoteProposalNotEq(t *testing.T) {
	cv, err := tmproto.Vote{Height: 1, Round: 1}.ToCanonicalVote("")
	require.NoError(t, err)
	p := CanonicalizeProposal("", &tmproto.Proposal{Height: 1, Round: 1})
	vb, err := proto.Marshal(&cv)
	require.NoError(t, err)
	pb, err := proto.Marshal(&p)
	require.NoError(t, err)
	require.NotEqual(t, vb, pb)
}

func TestVoteVerifySignature(t *testing.T) {
	type testCase struct {
		name        string
		modify      func(*tmproto.Vote)
		expectValid bool
	}
	testCases := []testCase{
		{
			name:        "correct",
			modify:      func(_ *tmproto.Vote) {},
			expectValid: true,
		},
		{
			name: "wrong state id",
			modify: func(v *tmproto.Vote) {
				v.BlockID.StateID[0] = ^v.BlockID.StateID[0]
			},
			expectValid: false,
		},
		{
			name: "wrong block hash",
			modify: func(v *tmproto.Vote) {
				v.BlockID.Hash[0] = ^v.BlockID.Hash[0]
			},
			expectValid: false,
		},
		{
			name: "wrong block signature",
			modify: func(v *tmproto.Vote) {
				v.BlockSignature[0] = ^v.BlockSignature[0]
			},
			expectValid: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			quorumHash := make([]byte, 32)
			privVal := NewMockPVForQuorum(quorumHash)
			pubkey, err := privVal.GetPubKey(context.Background(), quorumHash)
			require.NoError(t, err)

			vote := examplePrecommit(t)
			v := vote.ToProto()
			quorumType := btcjson.LLMQType_5_60
			signID := VoteBlockSignID("test_chain_id", v, quorumType, quorumHash)

			// sign it
			err = privVal.SignVote(ctx, "test_chain_id", quorumType, quorumHash, v, nil)
			require.NoError(t, err)

			// verify the same vote
			valid := pubkey.VerifySignatureDigest(signID, v.BlockSignature)
			require.True(t, valid)

			g1, err := bls.G1ElementFromBytes(pubkey.Bytes())
			require.NoError(t, err)
			signBytes, err := v.SignBytes("test_chain_id")
			require.NoError(t, err)
			t.Logf("-> pubkey: %s\n-> sign bytes: %x\n->  signID: %x\n-> signature: %x",
				g1.HexString(),
				signBytes,
				signID,
				v.BlockSignature)
			// serialize, deserialize and verify again....
			precommit := new(tmproto.Vote)
			bs, err := proto.Marshal(v)
			require.NoError(t, err)
			err = proto.Unmarshal(bs, precommit)
			require.NoError(t, err)

			// verify the transmitted vote
			if tc.modify != nil {
				tc.modify(precommit)
			}
			newSignID := VoteBlockSignID("test_chain_id", precommit, quorumType, quorumHash)
			valid = pubkey.VerifySignatureDigest(newSignID, precommit.BlockSignature)

			if tc.expectValid {
				assert.True(t, valid)
				assert.Equal(t, string(signID), string(newSignID))
			} else {
				assert.False(t, valid)
			}
		})
	}
}

// TestVoteExtension tests that the vote verification behaves correctly in each case
// of vote extension being set on the vote.
func TestVoteExtension(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	testCases := []struct {
		name             string
		extensions       VoteExtensions
		includeSignature bool
		expectError      bool
	}{
		{
			name: "valid THRESHOLD_RECOVER",
			extensions: VoteExtensionsFromProto(&tmproto.VoteExtension{
				Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
				Extension: []byte("extension")}),
			includeSignature: true,
			expectError:      false,
		},
		{
			name: "valid THRESHOLD_RECOVER_RAW plwdtx",
			extensions: VoteExtensionsFromProto(&tmproto.VoteExtension{
				Type: tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW,
				XSignRequestId: &tmproto.VoteExtension_SignRequestId{
					SignRequestId: []byte("\x06plwdtx"),
				},
				Extension: bytes.Repeat([]byte("extensio"), 4)}), // must be 32 bytes
			includeSignature: true,
			expectError:      false,
		},
		{
			name: "valid THRESHOLD_RECOVER_RAW dpevote",
			extensions: VoteExtensionsFromProto(&tmproto.VoteExtension{
				Type: tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW,
				XSignRequestId: &tmproto.VoteExtension_SignRequestId{
					SignRequestId: []byte("dpevote"),
				},
				Extension: bytes.Repeat([]byte("extensio"), 4)}), // must be 32 bytes
			includeSignature: true,
			expectError:      false,
		},
		{
			name: "no extension signature",
			extensions: VoteExtensionsFromProto(&tmproto.VoteExtension{
				Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
				Extension: []byte("extension")}),
			includeSignature: false,
			expectError:      true,
		},
		{
			name:             "empty extension",
			includeSignature: true,
			expectError:      false,
		},
	}

	logger := log.NewTestingLogger(t)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			height, round := int64(1), int32(0)
			quorumHash := crypto.RandQuorumHash()
			privVal := NewMockPVForQuorum(quorumHash)
			proTxHash, err := privVal.GetProTxHash(ctx)
			require.NoError(t, err)
			pk, err := privVal.GetPubKey(ctx, quorumHash)
			require.NoError(t, err)
			blockID := makeBlockID(rand.Bytes(crypto.HashSize), 1, rand.Bytes(crypto.HashSize), nil)

			vote := &Vote{
				ValidatorProTxHash: proTxHash,
				ValidatorIndex:     0,
				Height:             height,
				Round:              round,
				Type:               tmproto.PrecommitType,
				BlockID:            blockID,
				VoteExtensions:     tc.extensions,
			}
			v := vote.ToProto()
			err = privVal.SignVote(ctx, "test_chain_id", btcjson.LLMQType_5_60, quorumHash, v, logger)
			require.NoError(t, err)
			vote.BlockSignature = v.BlockSignature
			if tc.includeSignature {
				for i, ext := range v.VoteExtensions {
					vote.VoteExtensions[i].SetSignature(ext.Signature)
				}
			}

			err = vote.Verify("test_chain_id", btcjson.LLMQType_5_60, quorumHash, pk, proTxHash)
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
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
		t.Run(tt.name, func(_st *testing.T) {
			if rs := IsVoteTypeValid(tt.in); rs != tt.out {
				t.Errorf("got unexpected Vote type. Expected:\n%v\nGot:\n%v", rs, tt.out)
			}
		})
	}
}

func TestVoteVerify(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quorumHash := crypto.RandQuorumHash()
	privVal := NewMockPVForQuorum(quorumHash)
	proTxHash, err := privVal.GetProTxHash(ctx)
	require.NoError(t, err)

	quorumType := btcjson.LLMQType_5_60

	pubkey, err := privVal.GetPubKey(context.Background(), quorumHash)
	require.NoError(t, err)

	vote := examplePrevote(t)
	vote.ValidatorProTxHash = proTxHash

	pubKey := bls12381.GenPrivKey().PubKey()
	err = vote.Verify("test_chain_id", quorumType, quorumHash, pubKey, crypto.RandProTxHash())

	if assert.Error(t, err) {
		assert.Equal(t, ErrVoteInvalidValidatorProTxHash, err)
	}

	err = vote.Verify("test_chain_id", quorumType, quorumHash, pubkey, proTxHash)
	if assert.Error(t, err) {
		assert.ErrorIs(t, err, ErrVoteInvalidBlockSignature) // since block signatures are verified first
	}
}

func TestVoteString(t *testing.T) {
	testcases := map[string]struct {
		vote           *Vote
		expectedResult string
	}{
		"pre-commit": {
			vote:           examplePrecommit(t),
			expectedResult: preCommitTestStr,
		},
		"pre-vote": {
			vote:           examplePrevote(t),
			expectedResult: preVoteTestStr,
		},
		"absent vote": {
			expectedResult: absentVoteStr,
		},
		"nil vote": {
			vote: func() *Vote {
				v := examplePrecommit(t)
				v.BlockID.Hash = nil
				v.VoteExtensions = nil
				return v
			}(),
			expectedResult: nilVoteTestStr,
		},
	}

	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.expectedResult, tc.vote.String())
		})
	}
}

func signVote(
	ctx context.Context,
	t *testing.T,
	pv PrivValidator,
	chainID string,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	vote *Vote,
	logger log.Logger,
) {
	t.Helper()

	v := vote.ToProto()
	require.NoError(t, pv.SignVote(ctx, chainID, quorumType, quorumHash, v, logger))
	err := vote.PopulateSignsFromProto(v)
	require.NoError(t, err)
}

func TestValidVotes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	testCases := []struct {
		name         string
		vote         *Vote
		malleateVote func(*Vote)
	}{
		{"good prevote", examplePrevote(t), func(_ *Vote) {}},
		{"good precommit without vote extension", examplePrecommit(t), func(v *Vote) { v.VoteExtensions = nil }},
		{
			"good precommit with vote extension",
			examplePrecommit(t), func(v *Vote) {
				v.VoteExtensions[0] = VoteExtensionFromProto(tmproto.VoteExtension{
					Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
					Extension: []byte("extension"),
					Signature: make([]byte, SignatureSize),
				})
			},
		},
	}
	for _, tc := range testCases {
		quorumHash := crypto.RandQuorumHash()
		privVal := NewMockPVForQuorum(quorumHash)
		signVote(ctx, t, privVal, "test_chain_id", 0, quorumHash, tc.vote, nil)
		tc.malleateVote(tc.vote)
		require.NoError(t, tc.vote.ValidateBasic(), "ValidateBasic for %s", tc.name)
	}
}

func TestInvalidVotes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	testCases := []struct {
		name         string
		malleateVote func(*Vote)
	}{
		{"negative height", func(v *Vote) { v.Height = -1 }},
		{"negative round", func(v *Vote) { v.Round = -1 }},
		{"invalid block hash", func(v *Vote) { v.BlockID.Hash = v.BlockID.Hash[:crypto.DefaultHashSize-1] }},
		{"invalid state ID", func(v *Vote) { v.BlockID.StateID = v.BlockID.StateID[:crypto.DefaultAppHashSize-1] }},
		{"invalid block parts hash", func(v *Vote) { v.BlockID.PartSetHeader.Hash = v.BlockID.PartSetHeader.Hash[:crypto.DefaultHashSize-1] }},
		{"invalid block parts total", func(v *Vote) { v.BlockID.PartSetHeader.Total = 0 }},
		{"Invalid ProTxHash", func(v *Vote) { v.ValidatorProTxHash = make([]byte, 1) }},
		{"Invalid ValidatorIndex", func(v *Vote) { v.ValidatorIndex = -1 }},
		{"Invalid Signature", func(v *Vote) { v.BlockSignature = nil }},
		{"Too big Signature", func(v *Vote) { v.BlockSignature = make([]byte, SignatureSize+1) }},
	}
	quorumHash := crypto.RandQuorumHash()
	privVal := NewMockPVForQuorum(quorumHash)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			prevote := examplePrevote(t)
			signVote(ctx, t, privVal, "test_chain_id", 0, quorumHash, prevote, nil)
			tc.malleateVote(prevote)
			require.Error(t, prevote.ValidateBasic(), "ValidateBasic for %s in invalid prevote", tc.name)

			precommit := examplePrecommit(t)
			signVote(ctx, t, privVal, "test_chain_id", 0, quorumHash, precommit, nil)
			tc.malleateVote(precommit)
			require.Error(t, precommit.ValidateBasic(), "ValidateBasic for %s in invalid precommit", tc.name)
		})
	}
}

func TestInvalidPrevotes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quorumHash := crypto.RandQuorumHash()
	privVal := NewMockPVForQuorum(quorumHash)

	testCases := []struct {
		name         string
		malleateVote func(*Vote)
	}{
		{
			"vote extension present",
			func(v *Vote) {
				v.VoteExtensions = VoteExtensionsFromProto(&tmproto.VoteExtension{Extension: []byte("extension")})
			},
		},
		{
			"vote extension signature present",
			func(v *Vote) {
				v.VoteExtensions = VoteExtensionsFromProto(&tmproto.VoteExtension{Signature: []byte("signature")})
			},
		},
	}
	for _, tc := range testCases {
		prevote := examplePrevote(t)
		signVote(ctx, t, privVal, "test_chain_id", 0, quorumHash, prevote, nil)
		tc.malleateVote(prevote)
		require.Error(t, prevote.ValidateBasic(), "ValidateBasic for %s", tc.name)
	}
}

func TestInvalidPrecommitExtensions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quorumHash := crypto.RandQuorumHash()
	privVal := NewMockPVForQuorum(quorumHash)

	testCases := []struct {
		name         string
		malleateVote func(*Vote)
	}{
		{
			"vote extension present without signature", func(v *Vote) {
				v.VoteExtensions = VoteExtensionsFromProto(&tmproto.VoteExtension{Extension: []byte("extension")})
			},
		},
		// TODO(thane): Re-enable once https://github.com/tendermint/tendermint/issues/8272 is resolved
		//{"missing vote extension signature", func(v *Vote) { v.ExtensionSignature = nil }},
		{
			"oversized vote extension signature",
			func(v *Vote) {
				v.VoteExtensions = VoteExtensionsFromProto(&tmproto.VoteExtension{
					Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
					Signature: make([]byte, SignatureSize+1)})
			},
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			precommit := examplePrecommit(t)
			signVote(ctx, t, privVal, "test_chain_id", 0, quorumHash, precommit, nil)
			tc.malleateVote(precommit)
			// ValidateBasic ensures that vote extensions, if present, are well formed
			require.Error(t, precommit.ValidateBasic(), "ValidateBasic for %s", tc.name)
			require.Error(t, precommit.ValidateWithExtension(), "ValidateWithExtension for %s", tc.name)
		})
	}
}

// TestVoteExtensionsSignBytes checks if vote extension sign bytes are generated correctly.
//
// This test is synchronized with tests from github.com/dashpay/rs-tenderdash-abci
func TestVoteExtensionsSignBytes(t *testing.T) {
	expect := hexBytesFromString(t, "2a0a080102030405060708110100000000000000190200000000000000220a736f6d652d636861696e2801")
	ve := tmproto.VoteExtension{
		Extension: []byte{1, 2, 3, 4, 5, 6, 7, 8},
		Signature: []byte{},
		Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
	}
	signItem, err := VoteExtensionFromProto(ve).SignItem("some-chain", 1, 2, btcjson.LLMQType_TEST_PLATFORM, crypto.RandQuorumHash())
	assert.NoError(t, err)

	actual := signItem.Msg

	t.Logf("sign bytes: %x", actual)
	assert.EqualValues(t, expect, actual)
}

// TestVoteExtensionsSignBytesRaw checks vote extension sign bytes for a raw vote extension type.
//
// Given some vote extension, SignBytes or THRESHOLD_RECOVER_RAW returns that extension.
func TestVoteExtensionsSignBytesRaw(t *testing.T) {
	extension := bytes.Repeat([]byte{1, 2, 3, 4, 5, 6, 7, 8}, 4)
	quorumHash := bytes.Repeat([]byte{8, 7, 6, 5, 4, 3, 2, 1}, 4)
	expectedSignHash := []byte{0xe, 0x88, 0x8d, 0xa8, 0x97, 0xf1, 0xc0, 0xfd, 0x6a, 0xe8, 0x3b, 0x77, 0x9b, 0x5, 0xdd,
		0x28, 0xc, 0xe2, 0x58, 0xf6, 0x4c, 0x86, 0x1, 0x34, 0xfa, 0x4, 0x27, 0xe1, 0xaa, 0xab, 0x1a, 0xde}

	assert.Len(t, extension, 32)

	ve := tmproto.VoteExtension{
		Extension: extension,
		Signature: []byte{},
		Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW,
		XSignRequestId: &tmproto.VoteExtension_SignRequestId{
			SignRequestId: []byte("dpevote-someSignRequestID"),
		},
	}

	signItem, err := VoteExtensionFromProto(ve).SignItem("some-chain", 1, 2, btcjson.LLMQType_TEST_PLATFORM, quorumHash)
	assert.NoError(t, err)

	actual := signItem.SignHash

	t.Logf("sign hash: %x", actual)
	assert.EqualValues(t, expectedSignHash, actual)
}

func TestVoteProtobuf(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quorumHash := crypto.RandQuorumHash()
	privVal := NewMockPVForQuorum(quorumHash)
	vote := examplePrecommit(t)
	v := vote.ToProto()
	err := privVal.SignVote(ctx, "test_chain_id", 0, quorumHash, v, nil)
	vote.BlockSignature = v.BlockSignature
	require.NoError(t, err)

	testCases := []struct {
		msg                 string
		vote                *Vote
		convertsOk          bool
		passesValidateBasic bool
	}{
		{"success", vote, true, true},
		{"fail vote validate basic", &Vote{}, true, false},
	}
	for _, tc := range testCases {
		t.Run(tc.msg, func(t *testing.T) {
			protoProposal := tc.vote.ToProto()

			v, err := VoteFromProto(protoProposal)
			if tc.convertsOk {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}

			err = v.ValidateBasic()
			if tc.passesValidateBasic {
				require.NoError(t, err)
				require.Equal(t, tc.vote, v, tc.msg)
			} else {
				require.Error(t, err)
			}
		})
	}
}

var sink interface{}

func BenchmarkVoteSignBytes(b *testing.B) {
	protoVote := examplePrecommit(b).ToProto()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		var err error
		sink, err = protoVote.SignBytes("test_chain_id")
		require.NoError(b, err)
	}

	if sink == nil {
		b.Fatal("Benchmark did not run")
	}

	// Reset the sink.
	sink = (interface{})(nil)
}
