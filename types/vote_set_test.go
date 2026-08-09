package types

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/dash/llmq"
	tmrand "github.com/dashpay/tenderdash/libs/rand"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

func TestVoteSet_AddVote_Good(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	height, round := int64(1), int32(0)
	voteSet, _, privValidators := randVoteSet(ctx, t, height, round, tmproto.PrevoteType, 10)
	val0 := privValidators[0]

	val0ProTxHash, err := val0.GetProTxHash(ctx)
	require.NoError(t, err)

	assert.Nil(t, voteSet.GetByProTxHash(val0ProTxHash))
	assert.False(t, voteSet.BitArray().GetIndex(0))
	blockID, ok := voteSet.TwoThirdsMajority()
	assert.False(t, ok || !blockID.IsNil(), "there should be no 2/3 majority")

	vote := &Vote{
		ValidatorProTxHash: val0ProTxHash,
		ValidatorIndex:     0, // since privValidators are in order
		Height:             height,
		Round:              round,
		Type:               tmproto.PrevoteType,
		BlockID:            BlockID{},
	}
	_, err = signAddVote(ctx, val0, vote, voteSet)
	require.NoError(t, err)

	assert.NotNil(t, voteSet.GetByProTxHash(val0ProTxHash))
	assert.True(t, voteSet.BitArray().GetIndex(0))
	blockID, ok = voteSet.TwoThirdsMajority()
	assert.False(t, ok || !blockID.IsNil(), "there should be no 2/3 majority")
}

func TestVoteSet_AddVoteWithVerificationBudget_DenialIsNotInvalidSignature(t *testing.T) {
	ctx := context.Background()
	const (
		height = int64(1)
		round  = int32(0)
	)
	voteSet, _, privValidators := randVoteSet(ctx, t, height, round, tmproto.PrevoteType, 1)
	proTxHash, err := privValidators[0].GetProTxHash(ctx)
	require.NoError(t, err)
	vote := &Vote{
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     0,
		Height:             height,
		Round:              round,
		Type:               tmproto.PrevoteType,
	}
	budget := &recordingVerificationBudget{decisions: []bool{false}}

	added, err := voteSet.AddVoteWithVerificationBudget(vote, budget)

	require.False(t, added)
	require.ErrorIs(t, err, ErrVerificationBudgetExhausted)
	require.NotErrorIs(t, err, ErrVoteInvalidSignature)
	require.Equal(t, ErrVerificationBudgetExhausted, err,
		"local overload must be returned directly rather than classified as an invalid vote signature")
	require.Equal(t, []int{1}, budget.costs)
}

func TestVoteSet_AddVote_Bad(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	height, round := int64(1), int32(0)
	voteSet, _, privValidators := randVoteSet(ctx, t, height, round, tmproto.PrevoteType, 10)

	voteProto := &Vote{
		ValidatorProTxHash: nil,
		ValidatorIndex:     -1,
		Height:             height,
		Round:              round,
		Type:               tmproto.PrevoteType,
		BlockID:            BlockID{nil, PartSetHeader{}, RandStateID().Hash()},
	}

	// val0 votes for nil.
	{
		proTxHash, err := privValidators[0].GetProTxHash(ctx)
		require.NoError(t, err)
		vote := withValidator(voteProto, proTxHash, 0)
		added, err := signAddVote(ctx, privValidators[0], vote, voteSet)
		if !added || err != nil {
			t.Errorf("expected VoteSet.Add to succeed")
		}
	}

	// val0 votes again for some block.
	{
		proTxHash, err := privValidators[0].GetProTxHash(ctx)
		require.NoError(t, err)
		vote := withValidator(voteProto, proTxHash, 0)
		added, err := signAddVote(ctx, privValidators[0], withBlockHash(vote, tmrand.Bytes(32)), voteSet)
		if added || err == nil {
			t.Errorf("expected VoteSet.Add to fail, conflicting vote.")
		}
	}

	// val1 votes on another height
	{
		proTxHash, err := privValidators[1].GetProTxHash(ctx)
		require.NoError(t, err)
		vote := withValidator(voteProto, proTxHash, 1)
		added, err := signAddVote(ctx, privValidators[1], withHeight(vote, height+1), voteSet)
		if added || err == nil {
			t.Errorf("expected VoteSet.Add to fail, wrong height")
		}
	}

	// val2 votes on another round
	{
		proTxHash, err := privValidators[2].GetProTxHash(ctx)
		require.NoError(t, err)
		vote := withValidator(voteProto, proTxHash, 2)
		added, err := signAddVote(ctx, privValidators[2], withRound(vote, round+1), voteSet)
		if added || err == nil {
			t.Errorf("expected VoteSet.Add to fail, wrong round")
		}
	}

	// val3 votes of another type.
	{
		proTxHash, err := privValidators[3].GetProTxHash(ctx)
		require.NoError(t, err)
		vote := withValidator(voteProto, proTxHash, 3)
		added, err := signAddVote(ctx, privValidators[3], withType(vote, byte(tmproto.PrecommitType)), voteSet)
		if added || err == nil {
			t.Errorf("expected VoteSet.Add to fail, wrong type")
		}
	}

}

func TestVoteSet_2_3Majority(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	height, round := int64(1), int32(0)
	voteSet, _, privValidators := randVoteSet(ctx, t, height, round, tmproto.PrevoteType, 10)

	voteProto := &Vote{
		ValidatorProTxHash: nil, // NOTE: must fill in
		ValidatorIndex:     -1,  // NOTE: must fill in
		Height:             height,
		Round:              round,
		Type:               tmproto.PrevoteType,
		BlockID:            BlockID{},
	}
	// 6 out of 10 voted for nil.
	for i := int32(0); i < 6; i++ {
		proTxHash, err := privValidators[i].GetProTxHash(ctx)
		require.NoError(t, err)
		vote := withValidator(voteProto, proTxHash, i)
		_, err = signAddVote(ctx, privValidators[i], vote, voteSet)
		require.NoError(t, err)
	}
	blockID, ok := voteSet.TwoThirdsMajority()
	assert.False(t, ok || !blockID.IsNil(), "there should be no 2/3 majority")

	// 7th validator voted for some blockhash
	{
		proTxHash, err := privValidators[6].GetProTxHash(ctx)
		require.NoError(t, err)
		vote := withValidator(voteProto, proTxHash, 6)
		_, err = signAddVote(ctx, privValidators[6], withBlockHash(vote, tmrand.Bytes(32)), voteSet)
		require.NoError(t, err)
		blockID, ok = voteSet.TwoThirdsMajority()
		assert.False(t, ok || !blockID.IsNil(), "there should be no 2/3 majority")
	}

	// 8th validator voted for nil.
	{
		proTxHash, err := privValidators[7].GetProTxHash(ctx)
		require.NoError(t, err)
		vote := withValidator(voteProto, proTxHash, 7)
		_, err = signAddVote(ctx, privValidators[7], vote, voteSet)
		require.NoError(t, err)
		blockID, ok = voteSet.TwoThirdsMajority()
		assert.True(t, ok || blockID.IsNil(), "there should be 2/3 majority for nil")
	}
}

func TestVoteSet_2_3MajorityRedux(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	height, round := int64(1), int32(0)
	voteSet, _, privValidators := randVoteSet(ctx, t, height, round, tmproto.PrevoteType, 100)

	blockHash := crypto.CRandBytes(32)
	stateID := RandStateID()
	blockPartsTotal := uint32(123)
	blockPartSetHeader := PartSetHeader{blockPartsTotal, crypto.CRandBytes(32)}

	voteProto := &Vote{
		ValidatorProTxHash: nil, // NOTE: must fill in
		ValidatorIndex:     -1,  // NOTE: must fill in
		Height:             height,
		Round:              round,
		Type:               tmproto.PrevoteType,
		BlockID:            BlockID{blockHash, blockPartSetHeader, stateID.Hash()},
	}

	// 66 out of 100 voted for nil.
	for i := int32(0); i < 66; i++ {
		proTxHash, err := privValidators[i].GetProTxHash(ctx)
		require.NoError(t, err)
		vote := withValidator(voteProto, proTxHash, i)
		_, err = signAddVote(ctx, privValidators[i], vote, voteSet)
		require.NoError(t, err)
	}
	blockID, ok := voteSet.TwoThirdsMajority()
	assert.False(t, ok || !blockID.IsNil(),
		"there should be no 2/3 majority")

	// 67th validator voted for nil
	{
		proTxHash, err := privValidators[66].GetProTxHash(ctx)
		require.NoError(t, err)
		vote := withValidator(voteProto, proTxHash, 66)
		_, err = signAddVote(ctx, privValidators[66], withBlockHash(vote, nil), voteSet)
		require.NoError(t, err)
		blockID, ok = voteSet.TwoThirdsMajority()
		assert.False(t, ok || !blockID.IsNil(),
			"there should be no 2/3 majority: last vote added was nil")
	}

	// 68th validator voted for a different BlockParts PartSetHeader
	{
		proTxHash, err := privValidators[67].GetProTxHash(ctx)
		require.NoError(t, err)
		vote := withValidator(voteProto, proTxHash, 67)
		blockPartsHeader := PartSetHeader{blockPartsTotal, crypto.CRandBytes(32)}
		_, err = signAddVote(ctx, privValidators[67], withBlockPartSetHeader(vote, blockPartsHeader), voteSet)
		require.NoError(t, err)
		blockID, ok = voteSet.TwoThirdsMajority()
		assert.False(t, ok || !blockID.IsNil(),
			"there should be no 2/3 majority: last vote added had different PartSetHeader Hash")
	}

	// 69th validator voted for different BlockParts Total
	{
		proTxHash, err := privValidators[68].GetProTxHash(ctx)
		require.NoError(t, err)
		vote := withValidator(voteProto, proTxHash, 68)
		blockPartsHeader := PartSetHeader{blockPartsTotal + 1, blockPartSetHeader.Hash}
		_, err = signAddVote(ctx, privValidators[68], withBlockPartSetHeader(vote, blockPartsHeader), voteSet)
		require.NoError(t, err)
		blockID, ok = voteSet.TwoThirdsMajority()
		assert.False(t, ok || !blockID.IsNil(),
			"there should be no 2/3 majority: last vote added had different PartSetHeader Total")
	}

	// 70th validator voted for different CoreBlockHash
	{
		proTxHash, err := privValidators[69].GetProTxHash(ctx)
		require.NoError(t, err)
		vote := withValidator(voteProto, proTxHash, 69)
		_, err = signAddVote(ctx, privValidators[69], withBlockHash(vote, tmrand.Bytes(32)), voteSet)
		require.NoError(t, err)
		blockID, ok = voteSet.TwoThirdsMajority()
		assert.False(t, ok || !blockID.IsNil(),
			"there should be no 2/3 majority: last vote added had different CoreBlockHash")
	}

	// 71st validator voted for the right CoreBlockHash & BlockPartSetHeader
	{
		proTxHash, err := privValidators[70].GetProTxHash(ctx)
		require.NoError(t, err)
		vote := withValidator(voteProto, proTxHash, 70)
		_, err = signAddVote(ctx, privValidators[70], vote, voteSet)
		require.NoError(t, err)
		blockID, ok = voteSet.TwoThirdsMajority()
		assert.True(t, ok && blockID.Equals(BlockID{blockHash, blockPartSetHeader, stateID.Hash()}),
			"there should be 2/3 majority")
	}
}

func TestVoteSet_Conflicts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	height, round := int64(1), int32(0)
	voteSet, _, privValidators := randVoteSet(ctx, t, height, round, tmproto.PrevoteType, 4)
	blockHash1 := tmrand.Bytes(32)
	blockHash2 := tmrand.Bytes(32)

	voteProto := &Vote{
		ValidatorProTxHash: nil,
		ValidatorIndex:     -1,
		Height:             height,
		Round:              round,
		Type:               tmproto.PrevoteType,
		BlockID:            BlockID{},
	}

	val0ProTxHash, err := privValidators[0].GetProTxHash(ctx)
	require.NoError(t, err)

	// val0 votes for nil.
	{
		vote := withValidator(voteProto, val0ProTxHash, 0)
		added, err := signAddVote(ctx, privValidators[0], vote, voteSet)
		if !added || err != nil {
			t.Errorf("expected VoteSet.Add to succeed")
		}
	}

	// val0 votes again for blockHash1.
	{
		vote := withValidator(voteProto, val0ProTxHash, 0)
		added, err := signAddVote(ctx, privValidators[0], withBlockHash(vote, blockHash1), voteSet)
		assert.False(t, added, "conflicting vote")
		assert.Error(t, err, "conflicting vote")
	}

	// start tracking blockHash1
	blockID := withBlockHash(voteProto, blockHash1).BlockID
	err = voteSet.SetPeerMaj23("peerA", blockID, height, round)
	require.NoError(t, err)

	// val0 votes again for blockHash1.
	{
		vote := withValidator(voteProto, val0ProTxHash, 0)
		added, err := signAddVote(ctx, privValidators[0], withBlockHash(vote, blockHash1), voteSet)
		assert.True(t, added, "called SetPeerMaj23(), err=%s", err)
		assert.Error(t, err, "conflicting vote")
	}

	// attempt tracking blockHash2, should fail because already set for peerA.
	err = voteSet.SetPeerMaj23("peerA", BlockID{Hash: blockHash2, PartSetHeader: PartSetHeader{}}, height, round)
	require.Error(t, err)

	// val0 votes again for blockHash1.
	{
		vote := withValidator(voteProto, val0ProTxHash, 0)
		added, err := signAddVote(ctx, privValidators[0], withBlockHash(vote, blockHash2), voteSet)
		assert.False(t, added, "duplicate SetPeerMaj23() from peerA")
		assert.Error(t, err, "conflicting vote")
	}

	// val1 votes for blockHash1.
	{
		pvProTxHash, err := privValidators[1].GetProTxHash(ctx)
		assert.NoError(t, err)
		vote := withValidator(voteProto, pvProTxHash, 1)
		added, err := signAddVote(ctx, privValidators[1], withBlockHash(vote, blockHash1), voteSet)
		if !added || err != nil {
			t.Errorf("expected VoteSet.Add to succeed")
		}
	}

	// check
	if voteSet.HasTwoThirdsMajority() {
		t.Errorf("we shouldn't have 2/3 majority yet")
	}
	if voteSet.HasTwoThirdsAny() {
		t.Errorf("we shouldn't have 2/3 if any votes yet")
	}

	// val2 votes for blockHash2.
	{
		pvProTxHash, err := privValidators[2].GetProTxHash(context.Background())
		assert.NoError(t, err)
		vote := withValidator(voteProto, pvProTxHash, 2)
		added, err := signAddVote(ctx, privValidators[2], withBlockHash(vote, blockHash2), voteSet)
		if !added || err != nil {
			t.Errorf("expected VoteSet.Add to succeed")
		}
	}

	// check
	if voteSet.HasTwoThirdsMajority() {
		t.Errorf("we shouldn't have 2/3 majority yet")
	}
	if !voteSet.HasTwoThirdsAny() {
		t.Errorf("we should have 2/3 if any votes")
	}

	// now attempt tracking blockHash1
	err = voteSet.SetPeerMaj23("peerB", BlockID{Hash: blockHash1, PartSetHeader: PartSetHeader{}}, height, round)
	require.NoError(t, err)

	// val2 votes for blockHash1.
	{
		pvProTxHash, err := privValidators[2].GetProTxHash(ctx)
		assert.NoError(t, err)
		vote := withValidator(voteProto, pvProTxHash, 2)
		added, err := signAddVote(ctx, privValidators[2], withBlockHash(vote, blockHash1), voteSet)
		assert.True(t, added)
		assert.Error(t, err, "conflicting vote")
	}

	// check
	if !voteSet.HasTwoThirdsMajority() {
		t.Errorf("we should have 2/3 majority for blockHash1")
	}
	blockIDMaj23, _ := voteSet.TwoThirdsMajority()
	if !bytes.Equal(blockIDMaj23.Hash, blockHash1) {
		t.Errorf("got the wrong 2/3 majority blockhash")
	}
	if !voteSet.HasTwoThirdsAny() {
		t.Errorf("we should have 2/3 if any votes")
	}
}

func TestVoteSet_MakeCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	height, round := int64(1), int32(0)
	voteSet, _, privValidators := randVoteSet(ctx, t, height, round, tmproto.PrecommitType, 10)
	blockHash, blockPartSetHeader := crypto.CRandBytes(32), PartSetHeader{123, crypto.CRandBytes(32)}
	stateID := RandStateID()

	voteProto := &Vote{
		ValidatorProTxHash: nil,
		ValidatorIndex:     -1,
		Height:             height,
		Round:              round,
		Type:               tmproto.PrecommitType,
		BlockID:            BlockID{blockHash, blockPartSetHeader, stateID.Hash()},
	}

	// 6 out of 10 voted for some block.
	for i := int32(0); i < 6; i++ {
		pvProTxHash, err := privValidators[i].GetProTxHash(ctx)
		assert.NoError(t, err)
		vote := withValidator(voteProto, pvProTxHash, i)
		_, err = signAddVote(ctx, privValidators[i], vote, voteSet)
		if err != nil {
			t.Error(err)
		}
	}

	// MakeCommit should fail.
	assert.Panics(t, func() { voteSet.MakeCommit() }, "Doesn't have +2/3 majority")

	// 7th voted for some other block.
	{
		pvProTxHash, err := privValidators[6].GetProTxHash(ctx)
		assert.NoError(t, err)
		vote := withValidator(voteProto, pvProTxHash, 6)
		vote = withBlockHash(vote, tmrand.Bytes(32))
		vote = withBlockPartSetHeader(vote, PartSetHeader{123, tmrand.Bytes(32)})

		_, err = signAddVote(ctx, privValidators[6], vote, voteSet)
		require.NoError(t, err)
	}

	// The 8th voted like everyone else.
	{
		pvProTxHash, err := privValidators[7].GetProTxHash(ctx)
		assert.NoError(t, err)
		vote := withValidator(voteProto, pvProTxHash, 7)
		_, err = signAddVote(ctx, privValidators[7], vote, voteSet)
		require.NoError(t, err)
	}

	// The 9th voted for nil.
	{
		pvProTxHash, err := privValidators[8].GetProTxHash(ctx)
		assert.NoError(t, err)
		vote := withValidator(voteProto, pvProTxHash, 8)
		vote.BlockID = BlockID{}

		_, err = signAddVote(ctx, privValidators[8], vote, voteSet)
		require.NoError(t, err)
	}

	commit := voteSet.MakeCommit()

	// Ensure that Commit is good.
	if err := commit.ValidateBasic(); err != nil {
		t.Errorf("error in Commit.ValidateBasic(): %v", err)
	}
}

func TestVoteSet_LLMQType_50_60(t *testing.T) {
	const (
		height = int64(1)
		round  = int32(0)
	)
	testCases := []struct {
		llmqType      btcjson.LLMQType
		numValidators int
		threshold     int
	}{
		{
			llmqType:      btcjson.LLMQType(0), // "tendermint" algorithm
			numValidators: 40,
			threshold:     int(math.Floor(2.0/3.0*40)) + 1,
		},
		{
			llmqType:      btcjson.LLMQType_50_60,
			numValidators: 35,
			threshold:     30,
		},
		{
			llmqType:      btcjson.LLMQType(0),
			numValidators: 50,
			threshold:     34,
		},
		{
			llmqType:      btcjson.LLMQType_50_60,
			numValidators: 50,
			threshold:     30,
		},
	}

	for ti, tt := range testCases {
		name := strconv.Itoa(ti)
		t.Run(name, func(t *testing.T) {
			voteSet, valSet, privValidators := randVoteSetWithLLMQType(
				height,
				round,
				tmproto.PrevoteType,
				tt.numValidators,
				tt.llmqType,
				tt.threshold,
				nil,
			)
			assert.EqualValues(t, tt.threshold, valSet.QuorumTypeThresholdCount())
			assert.GreaterOrEqual(t, len(privValidators), tt.threshold+3,
				"need at least %d validators", tt.threshold+3)

			blockHash := crypto.CRandBytes(32)
			stateID := RandStateID()
			blockPartSetHeader := PartSetHeader{uint32(123), crypto.CRandBytes(32)}
			votedBlock := BlockID{blockHash, blockPartSetHeader, stateID.Hash()}

			// below threshold
			for i := 0; i < tt.threshold-1; i++ {
				blockMaj, anyMaj := castVote(t, votedBlock, height, round, privValidators, int32(i), voteSet)
				assert.False(t, blockMaj, "no block majority expected here: i=%d, threshold=%d", i, tt.threshold)
				assert.False(t, anyMaj, "no 'any' majority expected here: i=%d, threshold=%d", i, tt.threshold)
			}

			// we add null vote
			blockMaj, anyMaj := castVote(t, BlockID{}, height, round, privValidators, int32(tt.threshold), voteSet)
			assert.False(t, blockMaj, "no block majority expected after nil vote")
			assert.True(t, anyMaj, "'any' majority expected  after nil vote at threshold")

			// at threshold
			blockMaj, anyMaj = castVote(t, votedBlock, height, round, privValidators, int32(tt.threshold+1), voteSet)
			assert.True(t, blockMaj, "block majority expected")
			assert.True(t, anyMaj, "'any' majority expected")

			// above threshold
			blockMaj, anyMaj = castVote(t, votedBlock, height, round, privValidators, int32(tt.threshold+2), voteSet)
			assert.True(t, blockMaj, "block majority expected")
			assert.True(t, anyMaj, "'any' majority expected")
		})
	}
}

// Given a set of validators and a threshold defined in ValidatorParams,
// when votes are cast,
// then the threshold from ValidatorParams is respected.
func TestVoteSet_ValidatorParams_Threshold(t *testing.T) {
	const (
		height = int64(1)
		round  = int32(0)
	)
	testCases := []struct {
		llmqType      btcjson.LLMQType
		numValidators int
		threshold     int
	}{
		{ // single node network
			llmqType:      btcjson.LLMQType_100_67,
			numValidators: 1,
			threshold:     1,
		},
		{ // two node network
			llmqType:      btcjson.LLMQType_100_67,
			numValidators: 2,
			threshold:     2,
		},
		{ // full network but threshold 2
			llmqType:      btcjson.LLMQType_100_67,
			numValidators: 100,
			threshold:     2,
		},
		{ // full network but threshold 3
			llmqType:      btcjson.LLMQType_100_67,
			numValidators: 100,
			threshold:     3,
		},
		{ // normal network
			llmqType:      btcjson.LLMQType_100_67,
			numValidators: 100,
			threshold:     67,
		},
		{ // network below threshold
			llmqType:      btcjson.LLMQType_100_67,
			numValidators: 67,
			threshold:     67,
		},
	}

	for ti, tt := range testCases {
		name := strconv.Itoa(ti)
		t.Run(name, func(t *testing.T) {
			threshold := uint64(int64(tt.threshold) * DefaultDashVotingPower)
			params := ValidatorParams{
				VotingPowerThreshold: &threshold,
			}
			voteSet, _, privValidators := randVoteSetWithLLMQType(
				height,
				round,
				tmproto.PrevoteType,
				tt.numValidators,
				tt.llmqType,
				tt.threshold,
				&params,
			)
			blockHash := crypto.CRandBytes(32)
			stateID := RandStateID()
			blockPartSetHeader := PartSetHeader{uint32(123), crypto.CRandBytes(32)}
			votedBlock := BlockID{blockHash, blockPartSetHeader, stateID.Hash()}

			// below threshold
			for i := 0; i < tt.threshold-1; i++ {
				blockMaj, anyMaj := castVote(t, votedBlock, height, round, privValidators, int32(i), voteSet)
				assert.False(t, blockMaj, "no block majority expected here: i=%d, threshold=%d", i, tt.threshold)
				assert.False(t, anyMaj, "no 'any' majority expected here: i=%d, threshold=%d", i, tt.threshold)
			}

			// we add null vote
			if tt.numValidators > tt.threshold {
				// we add null vote
				blockMaj, anyMaj := castVote(t, BlockID{}, height, round, privValidators, int32(tt.threshold), voteSet)
				assert.False(t, blockMaj, "no block majority expected after nil vote")
				assert.True(t, anyMaj, "'any' majority expected  after nil vote at threshold")

			}
			if tt.numValidators > tt.threshold+1 {
				// at threshold
				blockMaj, anyMaj := castVote(t, votedBlock, height, round, privValidators, int32(tt.threshold+1), voteSet)
				assert.True(t, blockMaj, "block majority expected")
				assert.True(t, anyMaj, "'any' majority expected")
			}
			// above threshold
			if tt.numValidators > tt.threshold+2 {
				blockMaj, anyMaj := castVote(t, votedBlock, height, round, privValidators, int32(tt.threshold+2), voteSet)
				assert.True(t, blockMaj, "block majority expected")
				assert.True(t, anyMaj, "'any' majority expected")
			}
		})
	}
}

func TestVoteSet_json_marshal_nil(t *testing.T) {
	var vset *VoteSet
	jsonBytes, err := json.Marshal(vset)
	assert.Equal(t, "null", string(jsonBytes))
	assert.Nil(t, err)
}

func TestVoteSet_strings_nil(t *testing.T) {
	var vset *VoteSet
	assert.Equal(t, nilVoteSetString, vset.String())
	assert.Equal(t, nilVoteSetString, vset.BitArrayString())
	assert.Equal(t, nilVoteSetString, vset.String())
	assert.Equal(t, nilVoteSetString, vset.StringIndented(" "))
	assert.Equal(t, nilVoteSetString, vset.StringShort())
	assert.Zero(t, vset.Type())
	assert.Empty(t, vset.VoteStrings())
}

func castVote(
	t *testing.T,
	blockID BlockID,
	height int64,
	round int32,
	privValidators []PrivValidator,
	validatorID int32,
	voteSet *VoteSet,
) (twoThirdsMajority, hasTwoThirdsAny bool) {
	voteProto := &Vote{
		ValidatorProTxHash: nil, // NOTE: must fill in
		ValidatorIndex:     -1,  // NOTE: must fill in
		Height:             height,
		Round:              round,
		Type:               tmproto.PrevoteType,
		BlockID:            blockID,
	}
	ctx := context.Background()
	proTxHash, err := privValidators[validatorID].GetProTxHash(ctx)
	require.NoError(t, err)
	vote := withValidator(voteProto, proTxHash, validatorID)
	signed, err := signAddVote(ctx, privValidators[validatorID], vote, voteSet)
	require.True(t, signed)
	require.NoError(t, err)

	majorityBlock, twoThirdsMajority := voteSet.TwoThirdsMajority()
	assert.EqualValues(t, twoThirdsMajority, !majorityBlock.IsNil())
	return twoThirdsMajority, voteSet.HasTwoThirdsAny()
}

// NOTE: privValidators are in order
func randVoteSet(
	_ctx context.Context,
	t testing.TB,
	height int64,
	round int32,
	signedMsgType tmproto.SignedMsgType,
	numValidators int,
) (*VoteSet, *ValidatorSet, []PrivValidator) {
	t.Helper()
	valSet, mockPVs := RandValidatorSet(numValidators)
	return NewVoteSet("test_chain_id", height, round, signedMsgType, valSet),
		valSet,
		append([]PrivValidator(nil), mockPVs...)
}

func randVoteSetWithLLMQType(
	height int64,
	round int32,
	signedMsgType tmproto.SignedMsgType,
	numValidators int,
	llmqType btcjson.LLMQType,
	threshold int,
	params *ValidatorParams,
) (*VoteSet, *ValidatorSet, []PrivValidator) {
	valz := make([]*Validator, 0, numValidators)
	privValidators := make([]PrivValidator, 0, numValidators)
	ld := llmq.MustGenerate(crypto.RandProTxHashes(numValidators), llmq.WithThreshold(threshold))
	quorumHash := crypto.RandQuorumHash()
	iter := ld.Iter()
	for iter.Next() {
		proTxHash, qks := iter.Value()
		privValidators = append(privValidators, NewMockPVWithParams(
			qks.PrivKey,
			proTxHash,
			quorumHash,
			ld.ThresholdPubKey,
			false,
			false,
		))
		valz = append(valz, NewValidatorDefaultVotingPower(qks.PubKey, proTxHash))
	}

	sort.Sort(PrivValidatorsByProTxHash(privValidators))

	valSet := NewValidatorSet(valz, ld.ThresholdPubKey, llmqType, quorumHash, true, params)
	voteSet := NewVoteSet("test_chain_id", height, round, signedMsgType, valSet)

	return voteSet, valSet, privValidators
}

// Convenience: Return new vote with different validator address/index
func withValidator(vote *Vote, proTxHash ProTxHash, idx int32) *Vote {
	vote = vote.Copy()
	vote.ValidatorProTxHash = proTxHash
	vote.ValidatorIndex = idx
	return vote
}

// Convenience: Return new vote with different height
func withHeight(vote *Vote, height int64) *Vote {
	vote = vote.Copy()
	vote.Height = height
	return vote
}

// Convenience: Return new vote with different round
func withRound(vote *Vote, round int32) *Vote {
	vote = vote.Copy()
	vote.Round = round
	return vote
}

// Convenience: Return new vote with different type
func withType(vote *Vote, signedMsgType byte) *Vote {
	vote = vote.Copy()
	vote.Type = tmproto.SignedMsgType(signedMsgType)
	return vote
}

// Convenience: Return new vote with different blockHash and state ID
func withBlockHash(vote *Vote, blockHash []byte) *Vote {
	vote = vote.Copy()
	vote.BlockID.Hash = blockHash

	ts := uint64(time.Date(2022, 1, 2, 3, 4, 5, 6, time.UTC).UnixMilli())
	vote.BlockID.StateID = tmproto.StateID{
		AppVersion:            StateIDVersion,
		Height:                uint64(vote.Height),
		AppHash:               blockHash,
		CoreChainLockedHeight: 1,
		Time:                  ts,
	}.Hash()
	return vote
}

// Convenience: Return new vote with different blockParts
func withBlockPartSetHeader(vote *Vote, blockPartsHeader PartSetHeader) *Vote {
	vote = vote.Copy()
	vote.BlockID.PartSetHeader = blockPartsHeader
	return vote
}

func TestSetPeerMaj23Cap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	height, round := int64(1), int32(0)
	voteSet, _, _ := randVoteSet(ctx, t, height, round, tmproto.PrecommitType, 4)

	blockIDFor := func(i int) BlockID {
		hash := make([]byte, crypto.HashSize)
		binary.BigEndian.PutUint32(hash, uint32(i))
		partSetHash := make([]byte, crypto.HashSize)
		binary.BigEndian.PutUint32(partSetHash, uint32(i))
		stateID := make([]byte, crypto.HashSize)
		binary.BigEndian.PutUint32(stateID, uint32(i))
		return makeBlockID(hash, 1, partSetHash, stateID)
	}

	// Claims from the first maxPeerMaj23s distinct peers are accepted.
	for i := 0; i < maxPeerMaj23s; i++ {
		err := voteSet.SetPeerMaj23(strconv.Itoa(i), blockIDFor(i), height, round)
		require.NoError(t, err)
	}
	require.Len(t, voteSet.peerMaj23s, maxPeerMaj23s)

	// A claim from a previously unseen peer beyond the cap is dropped, not errored.
	err := voteSet.SetPeerMaj23(strconv.Itoa(maxPeerMaj23s), blockIDFor(maxPeerMaj23s), height, round)
	require.NoError(t, err)
	require.Len(t, voteSet.peerMaj23s, maxPeerMaj23s, "over-cap claim must not be stored")

	// votesByBlock growth is bounded by the same cap (only peer claims here).
	require.LessOrEqual(t, len(voteSet.votesByBlock), maxPeerMaj23s)

	// A repeat claim from a known peer is still a no-op (no error).
	err = voteSet.SetPeerMaj23("0", blockIDFor(0), height, round)
	require.NoError(t, err)

	// A conflicting claim from a known peer still errors as before.
	err = voteSet.SetPeerMaj23("0", blockIDFor(1), height, round)
	require.Error(t, err)
}

// thresholdVoteExtensionsOfLen returns n (0..2) threshold-recoverable vote
// extensions. It simulates the honest extension count (2) versus a Byzantine
// validator offering a different count (0 or 1).
func thresholdVoteExtensionsOfLen(t testing.TB, n int) VoteExtensions {
	if n <= 0 {
		return nil
	}
	all := MustVoteExtensionsFromProto(t,
		&tmproto.VoteExtension{
			Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW,
			Extension: crypto.Checksum([]byte("raw")),
		},
		&tmproto.VoteExtension{
			Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
			Extension: []byte("threshold"),
		},
	)
	return all[:n]
}

// signAddPrecommitWithExtCount builds, signs and adds to voteSet a precommit for
// blockID from the validator at idx, carrying extCount threshold vote extensions.
// It returns the result of VoteSet.AddVote (and may therefore panic if AddVote
// does - which the backstop test relies on).
func signAddPrecommitWithExtCount(
	ctx context.Context,
	t testing.TB,
	voteSet *VoteSet,
	privVal PrivValidator,
	idx, extCount int,
	blockID BlockID,
) (bool, error) {
	t.Helper()
	proTxHash, err := privVal.GetProTxHash(ctx)
	require.NoError(t, err)
	vote := &Vote{
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     int32(idx),
		Height:             voteSet.GetHeight(),
		Round:              voteSet.GetRound(),
		Type:               tmproto.PrecommitType,
		BlockID:            blockID,
		VoteExtensions:     thresholdVoteExtensionsOfLen(t, extCount),
	}
	return signAddVote(ctx, privVal, vote, voteSet)
}

// TestVoteSet_AddVote_InconsistentVoteExtensionCountDoesNotHalt is a regression
// test for an inconsistent vote-extension count halting the node.
//
// A single Byzantine validator can broadcast a precommit for the about-to-commit
// block carrying an extension count that differs from the honest count - either
// zero, or non-zero-but-wrong. The vote's own signatures are valid, so it passes
// every admission check and enters votesByBlock. Before the fix, once the quorum
// threshold was crossed the threshold-signature recoverer either returned a
// length-mismatch error or had too few consistent extension shares, and
// addVerifiedVote panicked - halting every node (a network-wide liveness DoS).
//
// Each case drives the real VoteSet.AddVote -> addVerifiedVote -> recovery path
// with the Byzantine vote added FIRST (so it is part of the minimal
// quorum-crossing set, the worst case) and asserts: (a) no panic, and (b)
// threshold recovery still succeeds using the count-consistent honest majority,
// producing a commit that verifies against the threshold public key.
func TestVoteSet_AddVote_InconsistentVoteExtensionCountDoesNotHalt(t *testing.T) {
	const (
		height        = int64(10)
		round         = int32(0)
		numValidators = 10
		byzantineIdx  = 0 // always added first
		honestCount   = 2 // deterministic honest ABCI ExtendVote count
	)

	testCases := []struct {
		name string
		// byzantineExtCount is the (inconsistent) extension count the Byzantine
		// validator at byzantineIdx casts; 0 means it withholds extensions.
		byzantineExtCount int
		// numVoters is how many validators cast a precommit (indices 0..numVoters-1,
		// index 0 being the Byzantine one). The honest count-consistent group must
		// still reach the recovery threshold (n*2/3+1 = 7 for n=10).
		numVoters int
	}{
		{
			name:              "zero-count Byzantine vote, all validators voted",
			byzantineExtCount: 0,
			numVoters:         numValidators,
		},
		{
			name:              "non-zero count mismatch, all validators voted",
			byzantineExtCount: 1,
			numVoters:         numValidators,
		},
		{
			// 1 Byzantine + 7 honest = 8 voters: the honest count-consistent group
			// reaches the threshold without every validator having voted, so this
			// does not rely on the all-present backstop.
			name:              "non-zero count mismatch, not all validators voted",
			byzantineExtCount: 1,
			numVoters:         8,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			voteSet, valSet, privValidators := randVoteSet(ctx, t, height, round, tmproto.PrecommitType, numValidators)
			blockID := makeBlockIDRandom()

			require.NotPanics(t, func() {
				for i := 0; i < tc.numVoters; i++ {
					extCount := honestCount
					if i == byzantineIdx {
						extCount = tc.byzantineExtCount
					}
					added, err := signAddPrecommitWithExtCount(ctx, t, voteSet, privValidators[i], i, extCount, blockID)
					require.NoError(t, err)
					require.True(t, added)
				}
			}, "an inconsistent vote-extension count must not halt the node")

			// (b) recovery succeeded using the count-consistent honest votes: the
			// block has a 2/3 majority and a valid commit can be produced.
			require.True(t, voteSet.HasTwoThirdsMajority(), "quorum must be reached despite the Byzantine vote")

			commit := voteSet.MakeCommit()
			require.NotNil(t, commit)
			require.Len(t, commit.ThresholdVoteExtensions, 2, "both honest threshold-recoverable extensions must be recovered")

			// The recovered threshold block and vote-extension signatures must verify
			// against the quorum's threshold public key.
			require.NoError(t, valSet.VerifyCommit(voteSet.ChainID(), blockID, height, commit))
		})
	}
}

// TestVoteSet_AddVote_SubThirdCommitGateUsesRecoveryThreshold guards against
// keying the canonical vote-extension count off the configurable commit gate.
//
// The VotingPowerThreshold override may set the commit gate below 1/3 of
// the total voting power. If the canonical count were chosen as the count backed
// by that gate, a Byzantine minority's count could clear the low bar and be
// selected (the smallest-count tie-break would even prefer it), starving recovery
// below the BLS/DKG threshold and halting the chain. The canonical count must
// instead be keyed off the fixed recovery threshold (QuorumTypeThresholdVotingPower),
// which a Byzantine minority can never reach.
func TestVoteSet_AddVote_SubThirdCommitGateUsesRecoveryThreshold(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const (
		height        = int64(10)
		round         = int32(0)
		numValidators = 10
		honestCount   = 2
	)

	voteSet, valSet, privValidators := randVoteSet(ctx, t, height, round, tmproto.PrecommitType, numValidators)

	// Sub-1/3 commit gate: one validator's power (100) == 10% of the total (1000),
	// far below the recovery threshold (7 validators == 700).
	valSet.VotingPowerThreshold = uint64(DefaultDashVotingPower)
	require.Less(t, valSet.QuorumVotingThresholdPower(), valSet.QuorumTypeThresholdVotingPower(),
		"test must exercise a commit gate below the recovery threshold")

	blockID := makeBlockIDRandom()

	require.NotPanics(t, func() {
		for i := 0; i < numValidators; i++ {
			extCount := honestCount
			if i == 0 {
				extCount = 1 // Byzantine: a single, validly-signed extension
			}
			added, err := signAddPrecommitWithExtCount(ctx, t, voteSet, privValidators[i], i, extCount, blockID)
			require.NoError(t, err)
			require.True(t, added)
		}
	}, "a sub-1/3 commit gate must not let a Byzantine count starve recovery")

	require.True(t, voteSet.HasTwoThirdsMajority())

	commit := voteSet.MakeCommit()
	require.NotNil(t, commit)
	require.Len(t, commit.ThresholdVoteExtensions, 2, "the honest count must be canonical, not the Byzantine one")
	require.NoError(t, valSet.VerifyCommit(voteSet.ChainID(), blockID, height, commit))
}

// TestVoteSet_AddVote_NoRecoverableExtensionCountPanics verifies that the
// retained hard-fail backstop still fires for the genuinely unattributable case:
// every validator has voted yet no extension count is backed by the recovery
// threshold voting power (here a > 1/3 split, 4 vs 6 of 10, with the recovery
// threshold at 7). This is a BFT-safety violation, so there is no safe way to
// continue and the node must fail hard rather than silently stall forever.
func TestVoteSet_AddVote_NoRecoverableExtensionCountPanics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const (
		height        = int64(10)
		round         = int32(0)
		numValidators = 10
	)

	voteSet, _, privValidators := randVoteSet(ctx, t, height, round, tmproto.PrecommitType, numValidators)
	blockID := makeBlockIDRandom()

	// 4 validators send 1 extension, 6 send 2; neither group reaches the recovery
	// threshold of 7, so no count is recoverable.
	extCountFor := func(i int) int {
		if i < 4 {
			return 1
		}
		return 2
	}

	sawPanic := false
	for i := 0; i < numValidators && !sawPanic; i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					sawPanic = true
				}
			}()
			added, err := signAddPrecommitWithExtCount(ctx, t, voteSet, privValidators[i], i, extCountFor(i), blockID)
			require.NoError(t, err)
			require.True(t, added)
		}()
	}

	require.True(t, sawPanic, "the backstop hard-fail must fire when no count reaches the recovery threshold and all have voted")
	require.False(t, voteSet.HasTwoThirdsMajority(), "no commit may be produced when recovery is impossible")
}

// TestVoteSet_AddVote_ByzantineLastWithGateAboveRecovery is a regression test for
// a Byzantine-ordering attack: when VotingPowerThreshold (the commit gate)
// is configured above the fixed BLS recovery threshold, a Byzantine attacker can
// place its votes last so that every post-gate triggering vote carries a
// non-canonical extension count. Before the fix, recoverThresholdSignsAndVerify
// built its QuorumSignData from the triggering vote, causing a length mismatch in
// VerifyVoteExtensions (triggering-vote ext count != recovered canonical count)
// even though threshold recovery had already succeeded. The mismatch rolled back
// maj23 on every subsequent vote until all validators had voted, at which point the
// backstop panic fired — a deterministic liveness DoS.
//
// Setup: 10 validators @ 100 power (total=1000), commit gate=800,
// BLS recovery threshold=700 (7-of-10).
//
//   - Honest votes 0-6 (ext count=2): sum=700, below gate (800) → no trigger.
//   - Byzantine votes 7-9 (ext count=1): each crosses/maintains gate, triggers
//     recovery; canonical count is 2 (7 honest votes hold >=700 power). With the
//     fix the triggering vote is no longer used for verification → no length
//     mismatch → recovery succeeds on vote 7 → maj23 committed, commit produced.
func TestVoteSet_AddVote_ByzantineLastWithGateAboveRecovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const (
		height         = int64(10)
		round          = int32(0)
		numValidators  = 10
		honestCount    = 2 // honest ABCI ExtendVote extension count
		byzantineCount = 1 // Byzantine extension count (wrong, but validly signed)
		// 7 honest voters × 100 power = 700 >= recovery threshold; added first.
		// The remaining 3 (indices 7-9) are Byzantine; added LAST so each one
		// triggers the commit gate (800) with a Byzantine ext-count=1 vote.
		numHonest = 7
	)

	voteSet, valSet, privValidators := randVoteSet(ctx, t, height, round, tmproto.PrecommitType, numValidators)
	blockID := makeBlockIDRandom()

	// Raise the commit gate above the BLS recovery threshold.
	// QuorumTypeThresholdVotingPower for LLMQType_TEST_V17 with 10 validators
	// is 7*DefaultDashVotingPower = 700. Setting VotingPowerThreshold to 800
	// places the commit gate at 800 > 700 (recovery threshold), satisfying the
	// attack precondition.
	voteSet.valSet.VotingPowerThreshold = uint64(8 * DefaultDashVotingPower) // 800
	require.Greater(t, voteSet.valSet.QuorumVotingThresholdPower(),
		voteSet.valSet.QuorumTypeThresholdVotingPower(),
		"precondition: commit gate must exceed the BLS recovery threshold")

	require.NotPanics(t, func() {
		// Step 1: add honest votes first. With 7 honest votes at 700 total power
		// the commit gate (800) is not yet crossed, so no recovery is attempted.
		for i := 0; i < numHonest; i++ {
			added, err := signAddPrecommitWithExtCount(ctx, t, voteSet, privValidators[i], i, honestCount, blockID)
			require.NoError(t, err)
			require.True(t, added)
		}
		require.False(t, voteSet.HasTwoThirdsMajority(),
			"quorum must not be reached after honest votes alone (below commit gate)")

		// Step 2: add Byzantine votes LAST. Each one crosses or maintains the
		// commit gate (800) and acts as the triggering vote with ext count = 1.
		// Before the fix, quorumDataSigns from the Byzantine vote (1 sign item)
		// mismatched the recovered sigs (2 sigs for canonical count 2), causing
		// a spurious verification error → maj23 rollback → eventual panic.
		for i := numHonest; i < numValidators; i++ {
			added, err := signAddPrecommitWithExtCount(ctx, t, voteSet, privValidators[i], i, byzantineCount, blockID)
			require.NoError(t, err)
			require.True(t, added)
		}
	}, "Byzantine-last gate>recovery attack must not panic")

	require.True(t, voteSet.HasTwoThirdsMajority(),
		"quorum must be reached: 7 honest (ext=2) votes have 700 power >= recovery threshold")

	commit := voteSet.MakeCommit()
	require.NotNil(t, commit)
	require.Len(t, commit.ThresholdVoteExtensions, honestCount,
		"canonical (honest) extension count must be recovered")
	require.NoError(t, valSet.VerifyCommit(voteSet.ChainID(), blockID, height, commit),
		"recovered threshold signatures must verify against the quorum public key")
}
