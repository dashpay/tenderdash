package types

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	tmmath "github.com/dashpay/tenderdash/libs/math"
	tmrand "github.com/dashpay/tenderdash/libs/rand"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

func TestValidatorSetBasic(t *testing.T) {
	// empty or nil validator lists are allowed,
	vset := NewValidatorSet([]*Validator{}, nil, btcjson.LLMQType_5_60, nil, true)

	assert.EqualValues(t, vset, vset.Copy())
	assert.False(t, vset.HasProTxHash([]byte("some val")))
	idx, val := vset.GetByProTxHash([]byte("some val"))
	assert.EqualValues(t, -1, idx)
	assert.Nil(t, val)
	val = vset.GetByIndex(-100)
	assert.Nil(t, val)
	val = vset.GetByIndex(0)
	assert.Nil(t, val)
	val = vset.GetByIndex(100)
	assert.Nil(t, val)
	assert.Zero(t, vset.Size())
	assert.Equal(t, int64(0), vset.TotalVotingPower())
	assert.Equal(t, tmbytes.HexBytes(nil), vset.Hash())
	// add
	val = randValidator()
	assert.NoError(t, vset.UpdateWithChangeSet([]*Validator{val}, val.PubKey, crypto.RandQuorumHash()))

	assert.True(t, vset.HasProTxHash(val.ProTxHash))
	idx, _ = vset.GetByProTxHash(val.ProTxHash)
	assert.EqualValues(t, 0, idx)
	val0 := vset.GetByIndex(0)
	assert.Equal(t, val.ProTxHash, val0.ProTxHash)
	assert.Equal(t, 1, vset.Size())
	assert.Equal(t, val.VotingPower, vset.TotalVotingPower())
	assert.NotNil(t, vset.Hash())

	// update
	val = randValidator()
	assert.NoError(t, vset.UpdateWithChangeSet([]*Validator{val}, val.PubKey, crypto.RandQuorumHash()))
	_, val = vset.GetByProTxHash(val.ProTxHash)
	val.PubKey = bls12381.GenPrivKey().PubKey()

}

func TestValidatorSetValidateBasic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quorumHash := crypto.RandQuorumHash()
	val, _ := randValidatorInQuorum(ctx, t, quorumHash)
	badValNoPublicKey := &Validator{ProTxHash: val.ProTxHash}
	badValNoProTxHash := &Validator{PubKey: val.PubKey}

	goodValSet, _ := RandValidatorSet(4)
	badValSet, _ := RandValidatorSet(4)
	badValSet.ThresholdPublicKey = val.PubKey

	testCases := []struct {
		testName string
		vals     ValidatorSet
		err      bool
		msg      string
	}{
		{
			testName: "Validator set needs members even with threshold public key",
			vals:     ValidatorSet{ThresholdPublicKey: bls12381.GenPrivKey().PubKey()},
			err:      true,
			msg:      "validator set is nil or empty",
		},
		{
			testName: "Validator set needs members",
			vals:     ValidatorSet{},
			err:      true,
			msg:      "validator set is nil or empty",
		},
		{
			testName: "Validator set needs members even with threshold public key and quorum hash",
			vals: ValidatorSet{
				Validators:         []*Validator{},
				ThresholdPublicKey: bls12381.GenPrivKey().PubKey(),
				QuorumHash:         crypto.RandQuorumHash(),
				HasPublicKeys:      true,
			},
			err: true,
			msg: "validator set is nil or empty",
		},
		{
			testName: "Validator set needs members even with quorum hash",
			vals: ValidatorSet{
				Validators:    []*Validator{},
				QuorumHash:    crypto.RandQuorumHash(),
				HasPublicKeys: true,
			},
			err: true,
			msg: "validator set is nil or empty",
		},
		{
			testName: "Validator set needs members even with quorum hash",
			vals: ValidatorSet{
				Validators:         []*Validator{val},
				ThresholdPublicKey: val.PubKey,
				QuorumHash:         crypto.RandQuorumHash(),
				HasPublicKeys:      true,
			},
			err: false,
		},
		{
			testName: "Validator in set has wrong public key for threshold",
			vals: ValidatorSet{
				Validators:         []*Validator{val},
				ThresholdPublicKey: bls12381.GenPrivKey().PubKey(),
				QuorumHash:         crypto.RandQuorumHash(),
				HasPublicKeys:      true,
			},
			err: true,
			msg: "thresholdPublicKey error: incorrect threshold public key",
		},
		{
			testName: "Validator in set has no public key",
			vals: ValidatorSet{
				Validators:         []*Validator{badValNoPublicKey},
				ThresholdPublicKey: bls12381.GenPrivKey().PubKey(),
				QuorumHash:         crypto.RandQuorumHash(),
				HasPublicKeys:      true,
			},
			err: true,
			msg: "invalid validator pub key #0: validator does not have a public key",
		},
		{
			testName: "Validator in set has no proTxHash",
			vals: ValidatorSet{
				Validators:         []*Validator{badValNoProTxHash},
				ThresholdPublicKey: bls12381.GenPrivKey().PubKey(),
				QuorumHash:         crypto.RandQuorumHash(),
				HasPublicKeys:      true,
			},
			err: true,
			msg: "invalid validator #0: validator does not have a provider transaction hash",
		},
		{
			testName: "Validator set needs quorum hash",
			vals: ValidatorSet{
				Validators:         []*Validator{val},
				ThresholdPublicKey: val.PubKey,
				HasPublicKeys:      true,
			},
			err: true,
			msg: "quorumHash error: quorum hash is not set",
		},
		{
			testName: "Validator set single val good",
			vals: ValidatorSet{
				Validators:         []*Validator{val},
				ThresholdPublicKey: val.PubKey,
				QuorumHash:         crypto.RandQuorumHash(),
				HasPublicKeys:      true,
			},
			err: false,
			msg: "",
		},
		{
			testName: "Validator set needs threshold public key",
			vals: ValidatorSet{
				Validators:    []*Validator{val},
				QuorumHash:    crypto.RandQuorumHash(),
				HasPublicKeys: true,
			},
			err: true,
			msg: "thresholdPublicKey error: threshold public key is not set",
		},
		{
			testName: "Validator set has incorrect threshold public key",
			vals:     *badValSet,
			err:      true,
			msg:      "thresholdPublicKey error: incorrect recovered threshold public key",
		},
		{
			testName: "Validator set is good",
			vals:     *goodValSet,
			err:      false,
			msg:      "",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			err := tc.vals.ValidateBasic()
			if tc.err {
				if assert.Error(t, err) {
					assert.True(t, strings.HasPrefix(err.Error(), tc.msg))
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCopy(t *testing.T) {
	vset, _ := RandValidatorSet(10)
	vsetHash := vset.Hash()
	if len(vsetHash) == 0 {
		t.Fatalf("ValidatorSet had unexpected zero hash")
	}

	vsetCopy := vset.Copy()
	vsetCopyHash := vsetCopy.Hash()

	if !bytes.Equal(vsetHash, vsetCopyHash) {
		t.Fatalf("ValidatorSet copy had wrong hash. Orig: %X, Copy: %X", vsetHash, vsetCopyHash)
	}
}

func BenchmarkValidatorSetCopy(b *testing.B) {
	b.StopTimer()
	vset := NewValidatorSet([]*Validator{}, nil, btcjson.LLMQType_5_60, nil, true)
	for i := 0; i < 1000; i++ {
		privKey := bls12381.GenPrivKey()
		pubKey := privKey.PubKey()

		quorumKey := bls12381.GenPrivKey().PubKey()
		ProTxHash := crypto.RandProTxHash()
		val := NewValidatorDefaultVotingPower(pubKey, ProTxHash)
		err := vset.UpdateWithChangeSet([]*Validator{val}, quorumKey, crypto.RandQuorumHash())
		require.NoError(b, err)
	}
	b.StartTimer()

	for i := 0; i < b.N; i++ {
		vset.Copy()
	}
}

func randPubKey() crypto.PubKey {
	pubKey := make(bls12381.PubKey, bls12381.PubKeySize)
	copy(pubKey, tmrand.Bytes(32))
	return bls12381.PubKey(tmrand.Bytes(32))
}

func randValidator() *Validator {
	address := RandValidatorAddress().String()
	val := NewValidator(randPubKey(), DefaultDashVotingPower, crypto.RandProTxHash(), address)
	return val
}

func randValidatorInQuorum(ctx context.Context, t *testing.T, quorumHash crypto.QuorumHash) (*Validator, PrivValidator) {
	privVal := NewMockPVForQuorum(quorumHash)
	proTxHash, err := privVal.GetProTxHash(ctx)
	if err != nil {
		panic(fmt.Errorf("could not retrieve proTxHash %w", err))
	}
	pubKey, err := privVal.GetPubKey(ctx, quorumHash)
	require.NoError(t, err)
	address := RandValidatorAddress().String()
	val := NewValidator(pubKey, DefaultDashVotingPower, proTxHash, address)
	return val, privVal
}

func TestEmptySet(t *testing.T) {

	var valList []*Validator
	valSet := NewValidatorSet(valList, bls12381.PubKey{}, btcjson.LLMQType_5_60, crypto.QuorumHash{}, true)

	// Add to empty set
	proTxHashes := []crypto.ProTxHash{crypto.Checksum([]byte("v1")), crypto.Checksum([]byte("v2"))}
	valSetAdd, _ := GenerateValidatorSet(NewValSetParam(proTxHashes))
	assert.NoError(t, valSet.UpdateWithChangeSet(valSetAdd.Validators, valSetAdd.ThresholdPublicKey, crypto.RandQuorumHash()))
	verifyValidatorSet(t, valSet)

	// Delete all validators from set
	v1 := NewTestRemoveValidatorGeneratedFromProTxHash(crypto.Checksum([]byte("v1")))
	v2 := NewTestRemoveValidatorGeneratedFromProTxHash(crypto.Checksum([]byte("v1")))
	delList := []*Validator{v1, v2}
	assert.Error(t, valSet.UpdateWithChangeSet(delList, bls12381.PubKey{}, crypto.RandQuorumHash()))

	// Attempt delete from empty set
	assert.Error(t, valSet.UpdateWithChangeSet(delList, bls12381.PubKey{}, crypto.RandQuorumHash()))

}

func TestUpdatesForNewValidatorSet(t *testing.T) {

	addresses12 := []crypto.Address{crypto.Checksum([]byte("v1")), crypto.Checksum([]byte("v2"))}

	valSet, _ := GenerateValidatorSet(NewValSetParam(addresses12))
	verifyValidatorSet(t, valSet)

	// Verify duplicates are caught in NewValidatorSet() and it panics
	v111 := NewTestValidatorGeneratedFromProTxHash(crypto.Checksum([]byte("v1")))
	v112 := NewTestValidatorGeneratedFromProTxHash(crypto.Checksum([]byte("v1")))
	v113 := NewTestValidatorGeneratedFromProTxHash(crypto.Checksum([]byte("v1")))
	valList := []*Validator{v111, v112, v113}
	assert.Panics(t, func() { NewValidatorSet(valList, bls12381.PubKey{}, btcjson.LLMQType_5_60, crypto.QuorumHash{}, true) })

	// Verify set including validator with voting power 0 cannot be created
	v1 := NewTestRemoveValidatorGeneratedFromProTxHash(crypto.Checksum([]byte("v1")))
	v2 := NewTestValidatorGeneratedFromProTxHash(crypto.Checksum([]byte("v2")))
	v3 := NewTestValidatorGeneratedFromProTxHash(crypto.Checksum([]byte("v3")))
	valList = []*Validator{v1, v2, v3}
	assert.Panics(t, func() { NewValidatorSet(valList, bls12381.PubKey{}, btcjson.LLMQType_5_60, crypto.QuorumHash{}, true) })

	// Verify set including validator with negative voting power cannot be created
	v1 = NewTestValidatorGeneratedFromProTxHash(crypto.Checksum([]byte("v1")))
	v2 = &Validator{
		VotingPower: -20,
		ProTxHash:   crypto.Checksum([]byte("v2")),
	}
	v3 = NewTestValidatorGeneratedFromProTxHash(crypto.Checksum([]byte("v3")))
	valList = []*Validator{v1, v2, v3}
	assert.Panics(t, func() { NewValidatorSet(valList, bls12381.PubKey{}, btcjson.LLMQType_5_60, crypto.QuorumHash{}, true) })

}

type testVal struct {
	name  string
	power int64
}

type testProTxHashVal struct {
	proTxHash crypto.ProTxHash
	power     int64
}

func permutation(valList []testVal) []testVal {
	if len(valList) == 0 {
		return nil
	}
	permList := make([]testVal, len(valList))
	perm := rand.Perm(len(valList))
	for i, v := range perm {
		permList[v] = valList[i]
	}
	return permList
}

func createNewValidatorList(testValList []testVal) []*Validator {
	valList := make([]*Validator, 0, len(testValList))
	for _, val := range testValList {
		valList = append(valList, NewTestValidatorGeneratedFromProTxHash(crypto.Checksum([]byte(val.name))))
	}
	sort.Sort(ValidatorsByProTxHashes(valList))
	return valList
}

func createNewValidatorSet(testValList []testVal) *ValidatorSet {
	opts := make([]ValSetParam, len(testValList))
	for i, val := range testValList {
		opts[i] = ValSetParam{
			ProTxHash:   crypto.Checksum([]byte(val.name)),
			VotingPower: val.power,
		}
	}
	vals, _ := GenerateValidatorSet(opts)
	return vals
}

func addValidatorsToValidatorSet(vals *ValidatorSet, testValList []testVal) ([]*Validator, crypto.PubKey) {
	addedProTxHashes := make([]ProTxHash, 0, len(testValList))
	removedProTxHashes := make([]ProTxHash, 0, len(testValList))
	removedVals := make([]*Validator, 0, len(testValList))
	combinedProTxHashes := make([]ProTxHash, 0, len(testValList)+len(vals.Validators))
	for _, val := range testValList {
		if val.power != 0 {
			valProTxHash := crypto.Checksum([]byte(val.name))
			_, value := vals.GetByProTxHash(valProTxHash)
			if value == nil {
				addedProTxHashes = append(addedProTxHashes, valProTxHash)
			}
		} else {
			valProTxHash := crypto.Checksum([]byte(val.name))
			_, value := vals.GetByProTxHash(valProTxHash)
			if value != nil {
				removedProTxHashes = append(removedProTxHashes, valProTxHash)
			}
			removedVals = append(removedVals, NewTestRemoveValidatorGeneratedFromProTxHash(crypto.Checksum([]byte(val.name))))
		}
	}
	originalProTxHashes := vals.GetProTxHashes()
	for _, oProTxHash := range originalProTxHashes {
		found := false
		for _, removedProTxHash := range removedProTxHashes {
			if bytes.Equal(oProTxHash.Bytes(), removedProTxHash.Bytes()) {
				found = true
			}
		}
		if !found {
			combinedProTxHashes = append(combinedProTxHashes, oProTxHash)
		}
	}
	combinedProTxHashes = append(combinedProTxHashes, addedProTxHashes...)
	if len(combinedProTxHashes) > 0 {
		rVals, _ := GenerateValidatorSet(NewValSetParam(combinedProTxHashes))
		rValidators := append(rVals.Validators, removedVals...) //nolint:gocritic
		return rValidators, rVals.ThresholdPublicKey
	}
	return removedVals, nil

}

func verifyValidatorSet(t *testing.T, valSet *ValidatorSet) {
	// verify that the capacity and length of validators is the same
	assert.Equal(t, len(valSet.Validators), cap(valSet.Validators))

	// verify that the set's total voting power has been updated
	tvp := int64(0)
	for _, v := range valSet.Validators {
		tvp += v.VotingPower
	}
	assert.Equal(t, tvp, valSet.TotalVotingPower())

	recoveredPublicKey, err := bls12381.RecoverThresholdPublicKeyFromPublicKeys(valSet.GetPublicKeys(), valSet.GetProTxHashesAsByteArrays())
	assert.NoError(t, err)
	assert.Equal(t, valSet.ThresholdPublicKey, recoveredPublicKey, "the validator set threshold public key must match the recovered public key")
}

func toTestProTxHashValList(valList []*Validator) []testProTxHashVal {
	testList := make([]testProTxHashVal, len(valList))
	for i, val := range valList {
		testList[i].proTxHash = val.ProTxHash
		testList[i].power = val.VotingPower
	}
	return testList
}

func switchToTestProTxHashValList(valList []testVal) []testProTxHashVal {
	testList := make([]testProTxHashVal, len(valList))
	for i, val := range valList {
		testList[i].proTxHash = crypto.Checksum([]byte(val.name))
		testList[i].power = val.power
	}
	return testList
}

func testValSet(nVals int) []testVal {
	vals := make([]testVal, nVals)
	for i := 0; i < nVals; i++ {
		vals[i] = testVal{fmt.Sprintf("v%d", i+1), DefaultDashVotingPower}
	}
	return vals
}

type valSetErrTestCase struct {
	startVals  []testVal
	updateVals []testVal
}

type valSetErrTestCaseWithErr struct {
	startVals  []testVal
	updateVals []testVal
	errString  string
}

func executeValSetErrTestCaseIgnoreThresholdPublicKey(t *testing.T, idx int, tt valSetErrTestCaseWithErr) {
	// create a new set and apply updates, keeping copies for the checks
	valSet := createNewValidatorSet(tt.startVals)
	valSetExpected := valSet.Copy()
	valList := createNewValidatorList(tt.updateVals)
	valListCopy := validatorListCopy(valList)
	err := valSet.UpdateWithChangeSet(valList, bls12381.GenPrivKey().PubKey(), crypto.RandQuorumHash())

	// for errors check the validator set has not been changed
	if assert.Error(t, err, "test %d", idx) {
		assert.Contains(t, err.Error(), tt.errString)
	}
	assert.Equal(t, valSetExpected, valSet, "test %v", idx)

	// check the parameter list has not changed
	assert.Equal(t, valList, valListCopy, "test %v", idx)
}

func executeValSetErrTestCase(t *testing.T, idx int, tt valSetErrTestCase) {
	// create a new set and apply updates, keeping copies for the checks
	valSet := createNewValidatorSet(tt.startVals)
	valSetCopy := valSet.Copy()
	valList, thresholdPublicKey := addValidatorsToValidatorSet(valSet, tt.updateVals)
	valListCopy := validatorListCopy(valList)
	err := valSet.UpdateWithChangeSet(valList, thresholdPublicKey, crypto.RandQuorumHash())

	// for errors check the validator set has not been changed
	assert.Error(t, err, "test %d", idx)
	assert.Equal(t, valSet, valSetCopy, "test %v", idx)

	// check the parameter list has not changed
	assert.Equal(t, valList, valListCopy, "test %v", idx)
}

func TestValSetUpdatesDuplicateEntries(t *testing.T) {
	testCases := []valSetErrTestCaseWithErr{
		// Duplicate entries in changes
		{ // first entry is duplicated change
			testValSet(2),
			[]testVal{{"v1", DefaultDashVotingPower}, {"v1", DefaultDashVotingPower}},
			"duplicate entry Validator",
		},
		{ // second entry is duplicated change
			testValSet(2),
			[]testVal{{"v2", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}},
			"duplicate entry Validator",
		},
		{ // change duplicates are separated by a valid change
			testValSet(2),
			[]testVal{{"v1", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}, {"v1", DefaultDashVotingPower}},
			"duplicate entry Validator",
		},
		{ // change duplicates are separated by a valid change
			testValSet(3),
			[]testVal{{"v1", DefaultDashVotingPower}, {"v3", DefaultDashVotingPower}, {"v1", DefaultDashVotingPower}},
			"duplicate entry Validator",
		},

		// Duplicate entries in remove
		{ // first entry is duplicated remove
			testValSet(2),
			[]testVal{{"v1", 0}, {"v1", 0}},
			"duplicate entry Validator",
		},
		{ // second entry is duplicated remove
			testValSet(2),
			[]testVal{{"v2", 0}, {"v2", 0}},
			"duplicate entry Validator",
		},
		{ // remove duplicates are separated by a valid remove
			testValSet(2),
			[]testVal{{"v1", 0}, {"v2", 0}, {"v1", 0}},
			"duplicate entry Validator",
		},
		{ // remove duplicates are separated by a valid remove
			testValSet(3),
			[]testVal{{"v1", 0}, {"v3", 0}, {"v1", 0}},
			"duplicate entry Validator",
		},

		{ // remove and update same val
			testValSet(2),
			[]testVal{{"v1", 0}, {"v2", DefaultDashVotingPower}, {"v1", DefaultDashVotingPower}},
			"duplicate entry Validator",
		},
		{ // duplicate entries in removes + changes
			testValSet(2),
			[]testVal{{"v1", 0}, {"v2", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}, {"v1", 0}},
			"duplicate entry Validator",
		},
		{ // duplicate entries in removes + changes
			testValSet(3),
			[]testVal{{"v1", 0}, {"v3", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}, {"v1", 0}},
			"duplicate entry Validator",
		},
	}

	for i, tt := range testCases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			executeValSetErrTestCaseIgnoreThresholdPublicKey(t, i, tt)
		})
	}
}

func TestValSetUpdatesOtherErrors(t *testing.T) {
	testCases := []valSetErrTestCase{
		{ // remove non-existing validator
			testValSet(2),
			[]testVal{{"v3", 0}},
		},
		{ // delete all validators
			[]testVal{{"v1", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}, {"v3", DefaultDashVotingPower}},
			[]testVal{{"v1", 0}, {"v2", 0}, {"v3", 0}},
		},
	}

	for i, tt := range testCases {
		executeValSetErrTestCase(t, i, tt)
	}
}

func TestValSetUpdatesBasicTestsExecute(t *testing.T) {
	valSetUpdatesBasicTests := []struct {
		startVals    []testVal
		updateVals   []testVal
		expectedVals []testVal
	}{
		{ // no changes
			testValSet(2),
			[]testVal{},
			testValSet(2),
		},
		{ // voting power changes
			testValSet(2),
			[]testVal{{"v2", DefaultDashVotingPower}, {"v1", DefaultDashVotingPower}},
			[]testVal{{"v1", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}},
		},
		{ // add new validators
			[]testVal{{"v2", DefaultDashVotingPower}, {"v1", DefaultDashVotingPower}},
			[]testVal{{"v4", DefaultDashVotingPower}, {"v3", DefaultDashVotingPower}},
			[]testVal{{"v1", DefaultDashVotingPower}, {"v4", DefaultDashVotingPower}, {"v3", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}},
		},
		{ // add new validator to middle
			[]testVal{{"v3", DefaultDashVotingPower}, {"v1", DefaultDashVotingPower}},
			[]testVal{{"v2", DefaultDashVotingPower}},
			[]testVal{{"v1", DefaultDashVotingPower}, {"v3", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}},
		},
		{ // add new validator to beginning
			[]testVal{{"v3", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}},
			[]testVal{{"v1", DefaultDashVotingPower}},
			[]testVal{{"v1", DefaultDashVotingPower}, {"v3", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}},
		},
		{ // delete validators
			[]testVal{{"v3", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}, {"v1", DefaultDashVotingPower}},
			[]testVal{{"v2", 0}},
			[]testVal{{"v1", DefaultDashVotingPower}, {"v3", DefaultDashVotingPower}},
		},
	}

	for i, tt := range valSetUpdatesBasicTests {
		// create a new set and apply updates, keeping copies for the checks
		valSet := createNewValidatorSet(tt.startVals)
		valList, thresholdPublicKey := addValidatorsToValidatorSet(valSet, tt.updateVals)
		err := valSet.UpdateWithChangeSet(valList, thresholdPublicKey, crypto.RandQuorumHash())
		assert.NoError(t, err, "test %d", i)

		valListCopy := validatorListCopy(valSet.Validators)
		// check that the voting power in the set's validators is not changing if the voting power
		// is changed in the list of validators previously passed as parameter to UpdateWithChangeSet.
		// this is to make sure copies of the validators are made by UpdateWithChangeSet.
		if len(valList) > 0 {
			valList[0].VotingPower++
			assert.Equal(t, toTestProTxHashValList(valListCopy), toTestProTxHashValList(valSet.Validators), "test %v", i)

		}

		// check the final validator list is as expected and the set is properly scaled and centered.
		assert.Equal(t, switchToTestProTxHashValList(tt.expectedVals), toTestProTxHashValList(valSet.Validators), "test %v", i)
		verifyValidatorSet(t, valSet)
	}
}

// Test that different permutations of an update give the same result.
func TestValSetUpdatesOrderIndependenceTestsExecute(t *testing.T) {
	// startVals - initial validators to create the set with
	// updateVals - a sequence of updates to be applied to the set.
	// updateVals is shuffled a number of times during testing to check for same resulting validator set.
	valSetUpdatesOrderTests := []struct {
		startVals  []testVal
		updateVals []testVal
	}{
		0: { // order of changes should not matter, the final validator sets should be the same
			[]testVal{{"v4", DefaultDashVotingPower}, {"v3", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}, {"v1", DefaultDashVotingPower}},
			[]testVal{{"v4", DefaultDashVotingPower}, {"v3", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}, {"v1", DefaultDashVotingPower}}},

		1: { // order of additions should not matter
			[]testVal{{"v2", DefaultDashVotingPower}, {"v1", DefaultDashVotingPower}},
			[]testVal{{"v3", DefaultDashVotingPower}, {"v4", DefaultDashVotingPower}, {"v5", DefaultDashVotingPower}, {"v6", DefaultDashVotingPower}}},

		2: { // order of removals should not matter
			[]testVal{{"v4", DefaultDashVotingPower}, {"v3", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}, {"v1", DefaultDashVotingPower}},
			[]testVal{{"v1", 0}, {"v3", 0}, {"v4", 0}}},

		3: { // order of mixed operations should not matter
			[]testVal{{"v4", DefaultDashVotingPower}, {"v3", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}, {"v1", DefaultDashVotingPower}},
			[]testVal{{"v1", 0}, {"v3", 0}, {"v2", 22}, {"v5", 50}, {"v4", 44}}},
	}

	for i, tt := range valSetUpdatesOrderTests {
		// create a new set and apply updates
		valSet := createNewValidatorSet(tt.startVals)
		valSetCopy := valSet.Copy()
		valList, thresholdPublicKey := addValidatorsToValidatorSet(valSet, tt.updateVals)
		assert.NoError(t, valSetCopy.UpdateWithChangeSet(valList, thresholdPublicKey, crypto.RandQuorumHash()))

		// save the result as expected for next updates
		valSetExp := valSetCopy.Copy()

		// perform at most 20 permutations on the updates and call UpdateWithChangeSet()
		n := len(tt.updateVals)
		maxNumPerms := tmmath.MinInt(20, n*n)
		for j := 0; j < maxNumPerms; j++ {
			// create a copy of original set and apply a random permutation of updates
			valSetCopy := valSet.Copy()
			valList, thresholdPublicKey := addValidatorsToValidatorSet(valSetCopy, permutation(tt.updateVals))

			// check there was no error and the set is properly scaled and centered.
			assert.NoError(t, valSetCopy.UpdateWithChangeSet(valList, thresholdPublicKey, crypto.RandQuorumHash()),
				"test %v failed for permutation %v", i, valList)
			verifyValidatorSet(t, valSetCopy)

			// verify the resulting test is same as the expected
			assert.Equal(t, valSetCopy.GetProTxHashes(), valSetExp.GetProTxHashes(),
				"test %v failed for permutation %v", i, valList)
		}
	}
}

// This tests the private function validator_set.go:applyUpdates() function, used only for additions and changes.
// Should perform a proper merge of updatedVals and startVals
func TestValSetApplyUpdatesTestsExecute(t *testing.T) {

	valSetUpdatesBasicTests := []struct {
		startVals    []testVal
		updateVals   []testVal
		expectedVals []testVal
	}{
		// additions
		0: { // prepend
			[]testVal{{"v4", DefaultDashVotingPower}, {"v5", DefaultDashVotingPower}},
			[]testVal{{"v1", DefaultDashVotingPower}},
			[]testVal{{"v1", DefaultDashVotingPower}, {"v4", DefaultDashVotingPower}, {"v5", DefaultDashVotingPower}}},
		1: { // append
			[]testVal{{"v4", DefaultDashVotingPower}, {"v5", DefaultDashVotingPower}},
			[]testVal{{"v6", DefaultDashVotingPower}},
			[]testVal{{"v6", DefaultDashVotingPower}, {"v4", DefaultDashVotingPower}, {"v5", DefaultDashVotingPower}}},
		2: { // insert
			[]testVal{{"v4", DefaultDashVotingPower}, {"v6", DefaultDashVotingPower}},
			[]testVal{{"v5", DefaultDashVotingPower}},
			[]testVal{{"v6", DefaultDashVotingPower}, {"v4", DefaultDashVotingPower}, {"v5", DefaultDashVotingPower}}},
		3: { // insert multi
			[]testVal{{"v4", DefaultDashVotingPower}, {"v6", DefaultDashVotingPower}, {"v9", DefaultDashVotingPower}},
			[]testVal{{"v5", DefaultDashVotingPower}, {"v7", DefaultDashVotingPower}, {"v8", DefaultDashVotingPower}},
			[]testVal{{"v8", DefaultDashVotingPower}, {"v7", DefaultDashVotingPower}, {"v6", DefaultDashVotingPower}, {"v9", DefaultDashVotingPower}, {"v4", DefaultDashVotingPower}, {"v5", DefaultDashVotingPower}}},
		// changes
		4: { // head
			[]testVal{{"v1", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}},
			[]testVal{{"v1", DefaultDashVotingPower}},
			[]testVal{{"v1", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}}},
		5: { // tail
			[]testVal{{"v1", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}},
			[]testVal{{"v2", DefaultDashVotingPower}},
			[]testVal{{"v1", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}}},
		6: { // middle
			[]testVal{{"v1", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}, {"v3", DefaultDashVotingPower}},
			[]testVal{{"v2", DefaultDashVotingPower}},
			[]testVal{{"v1", DefaultDashVotingPower}, {"v3", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}}},
		7: { // multi
			[]testVal{{"v1", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}, {"v3", DefaultDashVotingPower}},
			[]testVal{{"v1", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}, {"v3", DefaultDashVotingPower}},
			[]testVal{{"v1", DefaultDashVotingPower}, {"v3", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}}},
		// additions and changes
		8: {
			[]testVal{{"v1", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}},
			[]testVal{{"v1", DefaultDashVotingPower}, {"v3", DefaultDashVotingPower}, {"v4", DefaultDashVotingPower}},
			[]testVal{{"v1", DefaultDashVotingPower}, {"v4", DefaultDashVotingPower}, {"v3", DefaultDashVotingPower}, {"v2", DefaultDashVotingPower}}},
	}

	for i, tt := range valSetUpdatesBasicTests {
		// create a new validator set with the start values
		valSet := createNewValidatorSet(tt.startVals)
		// applyUpdates() with the update values
		valList := createNewValidatorList(tt.updateVals)

		valSet.applyUpdates(valList)

		// check the new list of validators for proper merge
		assert.Equal(t, toTestProTxHashValList(valSet.Validators), switchToTestProTxHashValList(tt.expectedVals), "test %v", i)
	}
}

func TestValidatorSetProtoBuf(t *testing.T) {
	valset, _ := RandValidatorSet(10)
	valset2, _ := RandValidatorSet(10)
	valset2.Validators[0] = &Validator{}

	valset3, _ := RandValidatorSet(10)
	valset3.Validators[0] = nil

	valset4, _ := RandValidatorSet(10)
	valset4.Validators[0] = &Validator{}

	testCases := []struct {
		msg      string
		v1       *ValidatorSet
		expPass1 bool
		expPass2 bool
	}{
		{"success", valset, true, true},
		{"fail valSet2, pubkey empty", valset2, false, false},
		{"fail nil Proposer", valset3, false, false},
		{"fail empty Proposer", valset4, false, false},
		{"fail empty valSet", &ValidatorSet{}, true, false},
		{"false nil", nil, true, false},
	}
	for _, tc := range testCases {
		protoValSet, err := tc.v1.ToProto()
		if tc.expPass1 {
			require.NoError(t, err, tc.msg)
		} else {
			require.Error(t, err, tc.msg)
		}

		valSet, err := ValidatorSetFromProto(protoValSet)
		if tc.expPass2 {
			require.NoError(t, err, tc.msg)
			require.EqualValues(t, tc.v1, valSet, tc.msg)
		} else {
			require.Error(t, err, tc.msg)
		}
	}
}

// -------------------------------------
// Benchmark tests
func BenchmarkUpdates(b *testing.B) {
	const (
		n = 100
		m = 2000
	)
	// init with n validators
	proTxHashes0 := make([]crypto.ProTxHash, n)
	for j := 0; j < n; j++ {
		proTxHashes0[j] = crypto.Checksum([]byte(fmt.Sprintf("v%d", j)))
	}
	valSet, _ := GenerateValidatorSet(NewValSetParam(proTxHashes0))

	proTxHashes1 := make([]crypto.ProTxHash, n+m)
	newValList := make([]*Validator, m)
	for j := 0; j < n+m; j++ {
		proTxHashes1[j] = []byte(fmt.Sprintf("v%d", j))
		if j >= n {
			newValList[j-n] = NewTestValidatorGeneratedFromProTxHash(crypto.Checksum([]byte(fmt.Sprintf("v%d", j))))
		}
	}
	valSet2, _ := GenerateValidatorSet(NewValSetParam(proTxHashes1))

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Add m validators to valSetCopy
		valSetCopy := valSet.Copy()
		assert.NoError(b, valSetCopy.UpdateWithChangeSet(newValList, valSet2.ThresholdPublicKey, crypto.RandQuorumHash()))
	}
}

func BenchmarkValidatorSet_VerifyCommit_Ed25519(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, n := range []int{1, 8, 64, 1024} {
		n := n
		var (
			chainID = "test_chain_id"
			h       = int64(3)
			blockID = makeBlockIDRandom()
		)
		b.Run(fmt.Sprintf("valset size %d", n), func(b *testing.B) {
			b.ReportAllocs()
			// generate n validators
			voteSet, valSet, vals := randVoteSet(ctx, b, h, 0, tmproto.PrecommitType, n)
			// create a commit with n validators
			commit, err := makeCommit(ctx, blockID, h, 0, voteSet, vals)
			require.NoError(b, err)
			for i := 0; i < b.N/n; i++ {
				err = valSet.VerifyCommit(chainID, blockID, h, commit)
				assert.NoError(b, err)
			}
		})
	}
}
