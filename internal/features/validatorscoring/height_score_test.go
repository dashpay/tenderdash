package validatorscoring_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/dashpay/dashd-go/btcjson"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	"github.com/dashpay/tenderdash/internal/features/validatorscoring"
	"github.com/dashpay/tenderdash/types"
)

//-------------------------------------------------------------------

func TestProposerSelection1(t *testing.T) {
	fooProTxHash := crypto.ProTxHash(crypto.Checksum([]byte("foo")))
	barProTxHash := crypto.ProTxHash(crypto.Checksum([]byte("bar")))
	bazProTxHash := crypto.ProTxHash(crypto.Checksum([]byte("baz")))
	vset := types.NewValidatorSet([]*types.Validator{
		types.NewTestValidatorGeneratedFromProTxHash(fooProTxHash),
		types.NewTestValidatorGeneratedFromProTxHash(barProTxHash),
		types.NewTestValidatorGeneratedFromProTxHash(bazProTxHash),
	}, bls12381.GenPrivKey().PubKey(), btcjson.LLMQType_5_60, crypto.RandQuorumHash(), true)
	var proposers []string

	vs, err := validatorscoring.NewValidatorScoringStrategy(types.ConsensusParams{}, vset, 0, 0, nil)
	require.NoError(t, err)

	for height := int64(0); height < 99; height++ {
		val := vs.MustGetProposer(height, 0)
		proposers = append(proposers, val.ProTxHash.ShortString())
	}
	expected := `2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B ` +
		`2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B ` +
		`2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B ` +
		`2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B ` +
		`2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B ` +
		`2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B ` +
		`2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B 2C26B4 BAA5A0 FCDE2B`
	if expected != strings.Join(proposers, " ") {
		t.Errorf("expected sequence of proposers was\n%v\nbut got \n%v", expected, strings.Join(proposers, " "))
	}
}

func TestProposerSelection2(t *testing.T) {
	proTxHashes := make([]crypto.ProTxHash, 3)
	addresses := make([]crypto.Address, 3)
	proTxHashes[0] = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	proTxHashes[1] = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}
	proTxHashes[2] = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3}
	addresses[0] = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}
	addresses[1] = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	addresses[2] = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}

	vals, _ := types.GenerateValidatorSet(types.NewValSetParam(proTxHashes))
	vs, err := validatorscoring.NewValidatorScoringStrategy(types.ConsensusParams{}, vals.Copy(), 0, 0, nil)
	require.NoError(t, err)

	height := 0

	for ; height < len(proTxHashes)*5; height++ {
		ii := (height) % len(proTxHashes)
		prop := vs.MustGetProposer(int64(height), 0)
		if !bytes.Equal(prop.ProTxHash, vals.Validators[ii].ProTxHash) {
			t.Fatalf("(%d): Expected %X. Got %X", height, vals.Validators[ii].ProTxHash, prop.ProTxHash)
		}
	}

	prop := vs.MustGetProposer(int64(height), 0)
	if !bytes.Equal(prop.ProTxHash, proTxHashes[0]) {
		t.Fatalf("Expected proposer with smallest pro_tx_hash to be first proposer. Got %X", prop.ProTxHash)
	}

	height++
	prop = vs.MustGetProposer(int64(height), 0)
	if !bytes.Equal(prop.ProTxHash, proTxHashes[1]) {
		t.Fatalf("Expected proposer with second smallest pro_tx_hash to be second proposer. Got %X", prop.ProTxHash)
	}
}

func TestProposerSelection3(t *testing.T) {
	proTxHashes := make([]crypto.ProTxHash, 4)
	proTxHashes[0] = crypto.Checksum([]byte("avalidator_address12"))
	proTxHashes[1] = crypto.Checksum([]byte("bvalidator_address12"))
	proTxHashes[2] = crypto.Checksum([]byte("cvalidator_address12"))
	proTxHashes[3] = crypto.Checksum([]byte("dvalidator_address12"))

	vset, _ := types.GenerateValidatorSet(types.NewValSetParam(proTxHashes))

	vs, err := validatorscoring.NewValidatorScoringStrategy(types.ConsensusParams{}, vset.Copy(), 0, 0, nil)
	require.NoError(t, err)

	proposerOrder := make([]*types.Validator, 4)
	for i := 0; i < 4; i++ {
		proposerOrder[i] = vs.MustGetProposer(int64(i), 0)
	}

	// i for the loop
	// j for the times
	// we should go in order for ever, despite some IncrementProposerPriority with times > 1
	var (
		i int
		j int32
	)
	vs, err = validatorscoring.NewValidatorScoringStrategy(types.ConsensusParams{}, vset.Copy(), 0, 0, nil)
	require.NoError(t, err)

	for ; i < 10000; i++ {
		got := vs.MustGetProposer(int64(i), 0).ProTxHash
		expected := proposerOrder[j%4].ProTxHash
		if !bytes.Equal(got, expected) {
			t.Fatalf(fmt.Sprintf("vset.Proposer (%X) does not match expected proposer (%X) for (%d, %d)", got, expected, i, j))
		}
	}
}

func TestAveragingInIncrementProposerPriority(t *testing.T) {
	// Test that the averaging works as expected inside of IncrementProposerPriority.
	// Each validator comes with zero voting power which simplifies reasoning about
	// the expected ProposerPriority.
	tcs := []struct {
		vs    types.ValidatorSet
		times int32
		avg   int64
	}{
		0: {types.ValidatorSet{
			Validators: []*types.Validator{
				{ProTxHash: []byte("a"), ProposerPriority: 1},
				{ProTxHash: []byte("b"), ProposerPriority: 2},
				{ProTxHash: []byte("c"), ProposerPriority: 3}}},
			1,
			2,
		},
		1: {types.ValidatorSet{
			Validators: []*types.Validator{
				{ProTxHash: []byte("a"), ProposerPriority: 10},
				{ProTxHash: []byte("b"), ProposerPriority: -10},
				{ProTxHash: []byte("c"), ProposerPriority: 1}}},
			// this should average twice but the average should be 0 after the first iteration
			// (voting power is 0 -> no changes)
			// 1/3 -> 0
			11, 0},
		2: {types.ValidatorSet{
			Validators: []*types.Validator{
				{ProTxHash: []byte("a"), ProposerPriority: 100},
				{ProTxHash: []byte("b"), ProposerPriority: -10},
				{ProTxHash: []byte("c"), ProposerPriority: 1}}},
			1,
			91 / 3,
		},
	}
	for i, tc := range tcs {
		// work on copy to have the old ProposerPriorities:
		vs, err := validatorscoring.NewValidatorScoringStrategy(types.ConsensusParams{}, tc.vs.Copy(), 0, 0, nil)
		require.NoError(t, err)
		require.NoError(t, vs.UpdateScores(int64(tc.times), 0))
		newVset := vs.ValidatorSet()
		for _, val := range tc.vs.Validators {
			_, updatedVal := newVset.GetByProTxHash(val.ProTxHash)
			assert.Equal(t, updatedVal.ProposerPriority, val.ProposerPriority-tc.avg, "test case: %v", i)
		}
	}
}
