package types

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/libs/protoio"
	tmrand "github.com/dashpay/tenderdash/libs/rand"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

func getTestProposal(t testing.TB) *Proposal {
	t.Helper()

	stamp, err := time.Parse(TimeFormat, "2018-02-11T07:09:22.765Z")
	require.NoError(t, err)

	ts := uint64(stamp.UnixMilli())

	stateID := tmproto.StateID{
		AppVersion:            StateIDVersion,
		Height:                12345,
		AppHash:               []byte("12345678901234567890123456789012"),
		CoreChainLockedHeight: math.MaxUint32,
		Time:                  ts,
	}

	return &Proposal{
		Height: 12345,
		Round:  23456,
		BlockID: BlockID{
			Hash:          []byte("--June_15_2020_amino_was_removed"),
			PartSetHeader: PartSetHeader{Total: 111, Hash: []byte("--June_15_2020_amino_was_removed")},
			StateID:       stateID.Hash(),
		},
		POLRound:  -1,
		Timestamp: stamp,

		CoreChainLockedHeight: 100,
	}
}

func TestProposalSignable(t *testing.T) {
	chainID := "test_chain_id"
	signBytes := ProposalBlockSignBytes(chainID, getTestProposal(t).ToProto())
	pb := CanonicalizeProposal(chainID, getTestProposal(t).ToProto())

	expected, err := protoio.MarshalDelimited(&pb)
	require.NoError(t, err)
	require.Equal(t, expected, signBytes, "Got unexpected sign bytes for Proposal")
}

func TestProposalString(t *testing.T) {
	str := getTestProposal(t).String()
	expected := `Proposal{12345/23456 (2D2D4A756E655F31355F323032305F616D696E6F5F7761735F72656D6F766564:111:2D2D4A756E65:E97D78757A53, -1) 000000000000 @ 2018-02-11T07:09:22.765Z}`
	assert.Equal(t, expected, str)
}

func TestProposalVerifySignature(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quorumHash := crypto.RandQuorumHash()
	privVal := NewMockPVForQuorum(quorumHash)
	pubKey, err := privVal.GetPubKey(ctx, quorumHash)
	require.NoError(t, err)

	prop := NewProposal(
		4, 1, 2, 2,
		BlockID{
			tmrand.Bytes(crypto.HashSize),
			PartSetHeader{777, tmrand.Bytes(crypto.HashSize)},
			RandStateID().Hash(),
		},
		tmtime.Now(),
	)
	p := prop.ToProto()
	signID := ProposalBlockSignID("test_chain_id", p, btcjson.LLMQType_5_60, quorumHash)

	// sign it
	_, err = privVal.SignProposal(ctx, "test_chain_id", btcjson.LLMQType_5_60, quorumHash, p)
	require.NoError(t, err)
	prop.Signature = p.Signature

	// verify the same proposal
	valid := pubKey.VerifySignatureDigest(signID, prop.Signature)
	require.True(t, valid)

	// serialize, deserialize and verify again....
	newProp := new(tmproto.Proposal)
	pb := prop.ToProto()

	bs, err := proto.Marshal(pb)
	require.NoError(t, err)

	err = proto.Unmarshal(bs, newProp)
	require.NoError(t, err)

	np, err := ProposalFromProto(newProp)
	require.NoError(t, err)

	// verify the transmitted proposal
	newSignID := ProposalBlockSignID("test_chain_id", pb, btcjson.LLMQType_5_60, quorumHash)
	require.Equal(t, string(signID), string(newSignID))
	valid = pubKey.VerifySignatureDigest(newSignID, np.Signature)
	require.True(t, valid)
}

func BenchmarkProposalWriteSignBytes(b *testing.B) {
	pbp := getTestProposal(b).ToProto()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ProposalBlockSignBytes("test_chain_id", pbp)
	}
}

func BenchmarkProposalSign(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quorumHash := crypto.RandQuorumHash()
	privVal := NewMockPVForQuorum(quorumHash)

	pbp := getTestProposal(b).ToProto()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := privVal.SignProposal(ctx, "test_chain_id", 0, quorumHash, pbp)
		if err != nil {
			b.Error(err)
		}
	}
}

func BenchmarkProposalVerifySignature(b *testing.B) {
	testProposal := getTestProposal(b)
	pbp := testProposal.ToProto()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quorumHash := crypto.RandQuorumHash()
	privVal := NewMockPVForQuorum(quorumHash)
	_, err := privVal.SignProposal(ctx, "test_chain_id", 0, quorumHash, pbp)
	require.NoError(b, err)
	pubKey, err := privVal.GetPubKey(ctx, quorumHash)
	require.NoError(b, err)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		pubKey.VerifySignature(ProposalBlockSignBytes("test_chain_id", pbp), testProposal.Signature)
	}
}

func TestProposalValidateBasic(t *testing.T) {
	quorumHash := crypto.RandQuorumHash()
	privVal := NewMockPVForQuorum(quorumHash)
	testCases := []struct {
		testName         string
		malleateProposal func(*Proposal)
		expectErr        bool
	}{
		{"Good Proposal", func(_ *Proposal) {}, false},
		{"Invalid Type", func(p *Proposal) { p.Type = tmproto.PrecommitType }, true},
		{"Invalid Height", func(p *Proposal) { p.Height = -1 }, true},
		{"Invalid Round", func(p *Proposal) { p.Round = -1 }, true},
		{"Invalid POLRound", func(p *Proposal) { p.POLRound = -2 }, true},
		{"Invalid BlockId", func(p *Proposal) {
			p.BlockID = BlockID{
				[]byte{1, 2, 3},
				PartSetHeader{111, []byte("blockparts")},
				RandStateID().Hash()}
		}, true},
		{"Invalid Signature", func(p *Proposal) {
			p.Signature = make([]byte, 0)
		}, true},
		{"Too big Signature", func(p *Proposal) {
			p.Signature = make([]byte, SignatureSize+1)
		}, true},
	}
	blockID := makeBlockID(
		crypto.Checksum([]byte("blockhash")),
		math.MaxInt32,
		crypto.Checksum([]byte("partshash")),
		nil,
	)

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			prop := NewProposal(
				4, 1, 2, 2,
				blockID, tmtime.Now())
			p := prop.ToProto()
			_, err := privVal.SignProposal(ctx, "test_chain_id", 0, quorumHash, p)
			prop.Signature = p.Signature
			require.NoError(t, err)
			tc.malleateProposal(prop)
			assert.Equal(t, tc.expectErr, prop.ValidateBasic() != nil, "Validate Basic had an unexpected result")
		})
	}
}

func TestProposalProtoBuf(t *testing.T) {
	proposal := NewProposal(
		1,
		1,
		2,
		3,
		makeBlockID(
			crypto.Checksum([]byte("hash")),
			2,
			crypto.Checksum([]byte("part_set_hash")),
			nil,
		),
		tmtime.Now(),
	)
	proposal.Signature = []byte("sig")
	proposal2 := NewProposal(1, 1, 2, 3, BlockID{}, tmtime.Now())

	testCases := []struct {
		msg     string
		p1      *Proposal
		expPass bool
	}{
		{"success", proposal, true},
		{"success", proposal2, false}, // blockID cannot be empty
		{"empty proposal failure validatebasic", &Proposal{}, false},
		{"nil proposal", nil, false},
	}
	for _, tc := range testCases {
		protoProposal := tc.p1.ToProto()

		p, err := ProposalFromProto(protoProposal)
		if tc.expPass {
			require.NoError(t, err)
			require.Equal(t, tc.p1, p, tc.msg)
		} else {
			require.Error(t, err)
		}
	}
}

func TestIsTimely(t *testing.T) {
	genesisTime, err := time.Parse(time.RFC3339, "2019-03-13T23:00:00Z")
	require.NoError(t, err)
	testCases := []struct {
		name         string
		proposalTime time.Time
		recvTime     time.Time
		precision    time.Duration
		msgDelay     time.Duration
		expectTimely bool
		round        int32
	}{
		// proposalTime - precision <= localTime <= proposalTime + msgDelay + precision
		{
			// Checking that the following inequality evaluates to true:
			// 0 - 2 <= 1 <= 0 + 1 + 2
			name:         "basic timely",
			proposalTime: genesisTime,
			recvTime:     genesisTime.Add(1 * time.Nanosecond),
			precision:    time.Nanosecond * 2,
			msgDelay:     time.Nanosecond,
			expectTimely: true,
		},
		{
			// Checking that the following inequality evaluates to false:
			// 0 - 2 <= 4 <= 0 + 1 + 2
			name:         "local time too large",
			proposalTime: genesisTime,
			recvTime:     genesisTime.Add(4 * time.Nanosecond),
			precision:    time.Nanosecond * 2,
			msgDelay:     time.Nanosecond,
			expectTimely: false,
		},
		{
			// Checking that the following inequality evaluates to false:
			// 4 - 2 <= 0 <= 4 + 2 + 1
			name:         "proposal time too large",
			proposalTime: genesisTime.Add(4 * time.Nanosecond),
			recvTime:     genesisTime,
			precision:    time.Nanosecond * 2,
			msgDelay:     time.Nanosecond,
			expectTimely: false,
		},
		{
			// Checking that the following inequality evaluates to true:
			// 0 - (2 * 2)  <= 4 <= 0 + (1 * 2) + 2
			name:         "message delay adapts after 10 rounds",
			proposalTime: genesisTime,
			recvTime:     genesisTime.Add(4 * time.Nanosecond),
			precision:    time.Nanosecond * 2,
			msgDelay:     time.Nanosecond,
			expectTimely: true,
			round:        10,
		},
		{
			// check that values that overflow time.Duration still correctly register
			// as timely when round relaxation applied.
			name:         "message delay fixed to not overflow time.Duration",
			proposalTime: genesisTime,
			recvTime:     genesisTime.Add(4 * time.Nanosecond),
			precision:    time.Nanosecond * 2,
			msgDelay:     time.Nanosecond,
			expectTimely: true,
			round:        5000,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			p := Proposal{
				Timestamp: testCase.proposalTime,
			}

			sp := SynchronyParams{
				Precision:    testCase.precision,
				MessageDelay: testCase.msgDelay,
			}

			ti := p.CheckTimely(testCase.recvTime, sp, testCase.round)
			assert.Equal(t, testCase.expectTimely, ti == 0)
		})
	}
}
