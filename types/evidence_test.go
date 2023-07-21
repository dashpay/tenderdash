package types

import (
	"context"
	"encoding/hex"
	"math"
	"testing"
	"time"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/bls12381"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
)

var defaultVoteTime = time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)

func TestEvidenceList(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ev := randomDuplicateVoteEvidence(ctx, t)
	evl := EvidenceList([]Evidence{ev})

	assert.NotNil(t, evl.Hash())
	assert.True(t, evl.Has(ev))
	assert.False(t, evl.Has(&DuplicateVoteEvidence{}))
}

// TestEvidenceListProtoBuf to ensure parity in protobuf output and input
func TestEvidenceListProtoBuf(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const chainID = "mychain"
	ev, err := NewMockDuplicateVoteEvidence(ctx, math.MaxInt64, time.Now(), chainID, btcjson.LLMQType_5_60, crypto.RandQuorumHash())
	require.NoError(t, err)
	data := EvidenceList{ev}
	testCases := []struct {
		msg      string
		data1    *EvidenceList
		expPass1 bool
		expPass2 bool
	}{
		{"success", &data, true, true},
		{"empty evidenceData", &EvidenceList{}, true, true},
		{"fail nil Data", nil, false, false},
	}

	for _, tc := range testCases {
		protoData, err := tc.data1.ToProto()
		if tc.expPass1 {
			require.NoError(t, err, tc.msg)
		} else {
			require.Error(t, err, tc.msg)
		}

		eviD := new(EvidenceList)
		err = eviD.FromProto(protoData)
		if tc.expPass2 {
			require.NoError(t, err, tc.msg)
			require.Equal(t, tc.data1, eviD, tc.msg)
		} else {
			require.Error(t, err, tc.msg)
		}
	}
}

func randomDuplicateVoteEvidence(ctx context.Context, t *testing.T) *DuplicateVoteEvidence {
	t.Helper()
	quorumHash := crypto.RandQuorumHash()
	val := NewMockPVForQuorum(quorumHash)
	stateID := RandStateID()
	blockID := makeBlockID(
		[]byte("blockhash"),
		1000, []byte("partshash"),
		stateID.Hash(),
	)
	blockID2 := makeBlockID(
		[]byte("blockhash2"),
		1000, []byte("partshash"),
		stateID.Hash(),
	)
	quorumType := btcjson.LLMQType_5_60
	const chainID = "mychain"
	const height = int64(10)
	return &DuplicateVoteEvidence{
		VoteA:            makeVote(ctx, t, val, chainID, 0, height, 2, 1, quorumType, quorumHash, blockID),
		VoteB:            makeVote(ctx, t, val, chainID, 0, height, 2, 1, quorumType, quorumHash, blockID2),
		TotalVotingPower: 3 * DefaultDashVotingPower,
		ValidatorPower:   DefaultDashVotingPower,
		Timestamp:        defaultVoteTime,
	}
}

func TestDuplicateVoteEvidence(t *testing.T) {
	const height = int64(13)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quorumType := btcjson.LLMQType_5_60
	ev, err := NewMockDuplicateVoteEvidence(ctx, height, time.Now(), "mock-chain-id", quorumType, crypto.RandQuorumHash())
	require.NoError(t, err)
	assert.Equal(t, ev.Hash(), crypto.Checksum(ev.Bytes()))
	assert.NotNil(t, ev.String())
	assert.Equal(t, ev.Height(), height)
}

func TestDuplicateVoteEvidenceValidation(t *testing.T) {
	quorumHash := crypto.RandQuorumHash()
	val := NewMockPVForQuorum(quorumHash)
	stateID := RandStateID()
	blockID := makeBlockID(
		crypto.Checksum([]byte("blockhash")),
		math.MaxInt32,
		crypto.Checksum([]byte("partshash")),
		stateID.Hash(),
	)
	blockID2 := makeBlockID(
		crypto.Checksum([]byte("blockhash2")),
		math.MaxInt32,
		crypto.Checksum([]byte("partshash")),
		stateID.Hash(),
	)
	quorumType := btcjson.LLMQType_5_60
	const chainID = "mychain"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	testCases := []struct {
		testName         string
		malleateEvidence func(*DuplicateVoteEvidence)
		expectErr        bool
	}{
		{"Good DuplicateVoteEvidence", func(ev *DuplicateVoteEvidence) {}, false},
		{"Nil vote A", func(ev *DuplicateVoteEvidence) { ev.VoteA = nil }, true},
		{"Nil vote B", func(ev *DuplicateVoteEvidence) { ev.VoteB = nil }, true},
		{"Nil votes", func(ev *DuplicateVoteEvidence) {
			ev.VoteA = nil
			ev.VoteB = nil
		}, true},
		{"Invalid vote type", func(ev *DuplicateVoteEvidence) {
			ev.VoteA = makeVote(
				ctx, t, val, chainID, math.MaxInt32, math.MaxInt64, math.MaxInt32, 0, quorumType,
				quorumHash, blockID2)
		}, true},
		{"Invalid vote order", func(ev *DuplicateVoteEvidence) {
			swap := ev.VoteA.Copy()
			ev.VoteA = ev.VoteB.Copy()
			ev.VoteB = swap
		}, true},
	}
	for _, tc := range testCases {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			const height int64 = math.MaxInt64
			vote1 := makeVote(ctx, t, val, chainID, math.MaxInt32, height, math.MaxInt32, 0x02, quorumType,
				quorumHash, blockID)
			vote2 := makeVote(ctx, t, val, chainID, math.MaxInt32, height, math.MaxInt32, 0x02, quorumType,
				quorumHash, blockID2)
			thresholdPublicKey, err := val.GetThresholdPublicKey(context.Background(), quorumHash)
			assert.NoError(t, err)
			valSet := NewValidatorSet(
				[]*Validator{val.ExtractIntoValidator(context.Background(), quorumHash)}, thresholdPublicKey, quorumType, quorumHash, true)
			ev, err := NewDuplicateVoteEvidence(vote1, vote2, defaultVoteTime, valSet)
			require.NoError(t, err)
			tc.malleateEvidence(ev)
			err = ev.ValidateBasic()
			if tc.expectErr {
				assert.Error(t, err, "Validate Basic had an unexpected result")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestMockEvidenceValidateBasic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	goodEvidence, err := NewMockDuplicateVoteEvidence(ctx, int64(1), time.Now(), "mock-chain-id", btcjson.LLMQType_5_60,
		crypto.RandQuorumHash())
	assert.NoError(t, err)
	assert.Nil(t, goodEvidence.ValidateBasic())
}

func makeVote(
	ctx context.Context,
	t *testing.T,
	val PrivValidator,
	chainID string,
	valIndex int32,
	height int64,
	round int32,
	step int,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	blockID BlockID,
) *Vote {
	proTxHash, err := val.GetProTxHash(ctx)
	require.NoError(t, err)
	v := &Vote{
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     valIndex,
		Height:             height,
		Round:              round,
		Type:               tmproto.SignedMsgType(step),
		BlockID:            blockID,
	}

	vpb := v.ToProto()
	err = val.SignVote(ctx, chainID, quorumType, quorumHash, vpb, nil)
	require.NoError(t, err)
	v.BlockSignature = vpb.BlockSignature
	return v
}

func TestEvidenceProto(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// -------- Votes --------
	quorumHash := crypto.RandQuorumHash()
	val := NewMockPVForQuorum(quorumHash)
	stateID := RandStateID().Hash()
	blockID := makeBlockID(crypto.Checksum([]byte("blockhash")), math.MaxInt32, crypto.Checksum([]byte("partshash")), stateID)
	blockID2 := makeBlockID(crypto.Checksum([]byte("blockhash2")), math.MaxInt32, crypto.Checksum([]byte("partshash")), stateID)
	quorumType := btcjson.LLMQType_5_60
	const chainID = "mychain"
	var height int64 = math.MaxInt64

	v := makeVote(ctx, t, val, chainID, math.MaxInt32, height, 1, 0x01, quorumType, quorumHash, blockID)
	v2 := makeVote(ctx, t, val, chainID, math.MaxInt32, height, 2, 0x01, quorumType, quorumHash, blockID2)

	tests := []struct {
		testName     string
		evidence     Evidence
		toProtoErr   bool
		fromProtoErr bool
	}{
		{"nil fail", nil, true, true},
		{"DuplicateVoteEvidence empty fail", &DuplicateVoteEvidence{}, false, true},
		{"DuplicateVoteEvidence nil voteB", &DuplicateVoteEvidence{VoteA: v, VoteB: nil}, false, true},
		{"DuplicateVoteEvidence nil voteA", &DuplicateVoteEvidence{VoteA: nil, VoteB: v}, false, true},
		{"DuplicateVoteEvidence success", &DuplicateVoteEvidence{VoteA: v2, VoteB: v}, false, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.testName, func(t *testing.T) {
			pb, err := EvidenceToProto(tt.evidence)
			if tt.toProtoErr {
				assert.Error(t, err, tt.testName)
				return
			}
			assert.NoError(t, err, tt.testName)

			evi, err := EvidenceFromProto(pb)
			if tt.fromProtoErr {
				assert.Error(t, err, tt.testName)
				return
			}
			require.Equal(t, tt.evidence, evi, tt.testName)
		})
	}
}

func TestEvidenceVectors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Votes for duplicateEvidence
	quorumType := btcjson.LLMQType_5_60
	quorumHash := make([]byte, crypto.QuorumHashSize)
	val := NewMockPVForQuorum(quorumHash)
	val.ProTxHash = make([]byte, crypto.ProTxHashSize)
	key := bls12381.GenPrivKeyFromSecret([]byte("it's a secret")) // deterministic key
	ts := uint64(time.Date(2022, 1, 2, 3, 4, 5, 6, time.UTC).UnixMilli())
	stateID := tmproto.StateID{
		AppVersion:            StateIDVersion,
		Height:                1,
		AppHash:               make([]byte, crypto.DefaultAppHashSize),
		CoreChainLockedHeight: 1,
		Time:                  ts,
	}.Hash()
	val.UpdatePrivateKey(context.Background(), key, quorumHash, key.PubKey(), 10)
	blockID := makeBlockID(crypto.Checksum([]byte("blockhash")), math.MaxInt32, crypto.Checksum([]byte("partshash")), stateID)
	blockID2 := makeBlockID(crypto.Checksum([]byte("blockhash2")), math.MaxInt32, crypto.Checksum([]byte("partshash")), stateID)

	const chainID = "mychain"
	v := makeVote(ctx, t, val, chainID, math.MaxInt32, math.MaxInt64, 1, 0x01, quorumType, quorumHash, blockID)
	v2 := makeVote(ctx, t, val, chainID, math.MaxInt32, math.MaxInt64, 2, 0x01, quorumType, quorumHash, blockID2)

	testCases := []struct {
		testName string
		evList   EvidenceList
		expBytes string
	}{
		{"duplicateVoteEvidence",
			EvidenceList{&DuplicateVoteEvidence{VoteA: v2, VoteB: v}},
			"87904f3525bfdb8474a18bc44fcadf76f63f0e7cabc3063f5eae8dcf0eb11d79",
		},
	}

	for _, tc := range testCases {
		tc := tc
		hash := tc.evList.Hash()
		require.Equal(t, tc.expBytes, hex.EncodeToString(hash), tc.testName)
	}
}
