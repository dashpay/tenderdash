package types

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/rs/zerolog"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	cryptoenc "github.com/dashpay/tenderdash/crypto/encoding"
	"github.com/dashpay/tenderdash/crypto/merkle"
	"github.com/dashpay/tenderdash/dash/llmq"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	tmmath "github.com/dashpay/tenderdash/libs/math"

	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

const (
	// MaxTotalVotingPower - the maximum allowed total voting power.
	// It needs to be sufficiently small to, in all cases:
	// 1. prevent clipping in incrementProposerPriority()
	// 2. let (diff+diffMax-1) not overflow in IncrementProposerPriority()
	// (Proof of 1 is tricky, left to the reader).
	// It could be higher, but this is sufficiently large for our purposes,
	// and leaves room for defensive purposes.
	MaxTotalVotingPower = int64(math.MaxInt64) / 8

	DefaultDashVotingPower = int64(100)

	// PriorityWindowSizeFactor - is a constant that when multiplied with the
	// total voting power gives the maximum allowed distance between validator
	// priorities.
	PriorityWindowSizeFactor = 2
)

// ErrTotalVotingPowerOverflow is returned if the total voting power of the
// resulting validator set exceeds MaxTotalVotingPower.
var (
	ErrTotalVotingPowerOverflow = fmt.Errorf("total voting power of resulting valset exceeds max %d", MaxTotalVotingPower)
	ErrValidatorSetNilOrEmpty   = errors.New("validator set is nil or empty")
)

// ValidatorSet represent a set of *Validator at a given height.
//
// The validators can be fetched by address or index.
// The index is in order of .ProTxHash, so the indices are fixed for all
// rounds of a given blockchain height - ie. the validators are sorted by their
// .ProTxHash (ascending).
//
// NOTE: Not goroutine-safe.
// NOTE: All get/set to validators should copy the value for safety.
type ValidatorSet struct {
	// NOTE: persisted via reflect, must be exported.
	Validators         []*Validator `json:"validators"`
	proposerIndex      int32
	ThresholdPublicKey crypto.PubKey     `json:"threshold_public_key"`
	QuorumHash         crypto.QuorumHash `json:"quorum_hash"`
	QuorumType         btcjson.LLMQType  `json:"quorum_type"`
	HasPublicKeys      bool              `json:"has_public_keys"`
}

// NewValidatorSet initializes a ValidatorSet by copying over the values from
// `valz`, a list of Validators. If valz is nil or empty, the new ValidatorSet
// will have an empty list of Validators.
//
// The addresses of validators in `valz` must be unique otherwise the function
// panics.
//
// Note the validator set size has an implied limit equal to that of the
// MaxVotesCount - commits by a validator set larger than this will fail
// validation.
func NewValidatorSet(valz []*Validator, newThresholdPublicKey crypto.PubKey, quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash, hasPublicKeys bool) *ValidatorSet {
	vals := &ValidatorSet{
		QuorumHash:    quorumHash,
		QuorumType:    quorumType,
		HasPublicKeys: hasPublicKeys,
	}
	err := vals.updateWithChangeSet(valz, false, newThresholdPublicKey, quorumHash)
	if err != nil {
		panic(fmt.Sprintf("Cannot create validator set: %v", err))
	}

	return vals
}

// NewValidatorSetCheckPublicKeys initializes a ValidatorSet the same way as NewValidatorSet does,
// but determines if the public keys are present.
func NewValidatorSetCheckPublicKeys(
	valz []*Validator,
	newThresholdPublicKey crypto.PubKey,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
) *ValidatorSet {
	hasPublicKeys := true
	for _, val := range valz {
		if val.PubKey == nil || len(val.PubKey.Bytes()) == 0 {
			hasPublicKeys = false
		}
	}
	return NewValidatorSet(valz, newThresholdPublicKey, quorumType, quorumHash, hasPublicKeys)
}

// NewEmptyValidatorSet initializes a ValidatorSet with no validators
func NewEmptyValidatorSet() *ValidatorSet {
	return NewValidatorSet(nil, nil, 0, nil, false)
}

func (vals *ValidatorSet) ValidateBasic() error {
	if vals.IsNilOrEmpty() {
		return ErrValidatorSetNilOrEmpty
	}

	nVals, err := tmmath.SafeConvertInt32(int64(vals.Size()))
	if err != nil {
		return fmt.Errorf("failed to convert int to int32: %w", err)
	}

	if vals.proposerIndex >= nVals {
		return fmt.Errorf("validator set proposer index %d out of range, expected < %d", vals.proposerIndex, vals.Size())
	}

	for idx, val := range vals.Validators {
		if err := val.ValidateBasic(); err != nil {
			return fmt.Errorf("invalid validator #%d: %w", idx, err)
		}
		// We should only validate the public keys of validators if our local node is in the validator set
		if vals.HasPublicKeys {
			if err := val.ValidatePubKey(); err != nil {
				return fmt.Errorf("invalid validator pub key #%d: %w", idx, err)
			}
		}
	}

	if err := vals.ThresholdPublicKeyValid(); err != nil {
		return fmt.Errorf("thresholdPublicKey error: %w", err)
	}

	if err := vals.QuorumHashValid(); err != nil {
		return fmt.Errorf("quorumHash error: %w", err)
	}

	if err := vals.Proposer().ValidateBasic(); err != nil {
		return fmt.Errorf("proposer failed validate basic, error: %w", err)
	}

	return nil
}

func (vals *ValidatorSet) Equals(other *ValidatorSet) bool {
	if vals == nil && other == nil {
		return true
	}
	if vals == nil || other == nil {
		return false
	}
	if !vals.ThresholdPublicKey.Equals(other.ThresholdPublicKey) {
		return false
	}
	if !bytes.Equal(vals.QuorumHash, other.QuorumHash) {
		return false
	}
	if vals.QuorumType != other.QuorumType {
		return false
	}
	if len(vals.Validators) != len(other.Validators) {
		return false
	}
	for i, val := range vals.Validators {
		if !bytes.Equal(val.Bytes(), other.Validators[i].Bytes()) {
			return false
		}
	}
	return true
}

// IsNilOrEmpty returns true if validator set is nil or empty.
func (vals *ValidatorSet) IsNilOrEmpty() bool {
	return vals == nil || len(vals.Validators) == 0
}

// ThresholdPublicKeyValid returns true if threshold public key is valid.
func (vals *ValidatorSet) ThresholdPublicKeyValid() error {
	if vals.ThresholdPublicKey == nil {
		return errors.New("threshold public key is not set")
	}
	if len(vals.ThresholdPublicKey.Bytes()) != bls12381.PubKeySize {
		return errors.New("threshold public key is wrong size")
	}
	if len(vals.Validators) == 1 && vals.HasPublicKeys {
		if vals.Validators[0].PubKey == nil {
			return errors.New("validator public key is not set")
		}
		if !vals.Validators[0].PubKey.Equals(vals.ThresholdPublicKey) {
			return errors.New("incorrect threshold public key")
		}
	} else if len(vals.Validators) > 1 && vals.HasPublicKeys {
		// if we have validators and our node is in the validator set then verify the recovered threshold public key
		recoveredThresholdPublicKey, err := bls12381.RecoverThresholdPublicKeyFromPublicKeys(
			vals.GetPublicKeys(),
			vals.GetProTxHashesAsByteArrays(),
		)
		if err != nil {
			return err
		} else if !recoveredThresholdPublicKey.Equals(vals.ThresholdPublicKey) {
			return fmt.Errorf(
				"incorrect recovered threshold public key recovered %s expected %s keys %v proTxHashes %v",
				recoveredThresholdPublicKey.String(),
				vals.ThresholdPublicKey.String(),
				vals.GetPublicKeys(),
				vals.GetProTxHashesAsByteArrays(),
			)
		}
	}
	return nil
}

// QuorumHashValid returns true if quorum hash is valid.
func (vals *ValidatorSet) QuorumHashValid() error {
	if vals.QuorumHash == nil {
		return errors.New("quorum hash is not set")
	}
	if len(vals.QuorumHash.Bytes()) != crypto.DefaultHashSize {
		return errors.New("quorum hash is wrong size")
	}
	return nil
}

// Proposer returns the proposer of the validator set.
//
// Panics on empty validator set.
func (vals *ValidatorSet) Proposer() *Validator {
	if vals.IsNilOrEmpty() {
		panic("empty validator set")
	}
	return vals.GetByIndex(vals.proposerIndex)
}

// SetProposer sets the proposer of the validator set.
func (vals *ValidatorSet) SetProposer(newProposer ProTxHash) error {
	idx, _ := vals.GetByProTxHash(newProposer)
	if idx < 0 {
		return fmt.Errorf("proposer %X not found in validator set", newProposer)
	}

	vals.proposerIndex = idx
	return nil
}

// increaseProposerIndex increments the proposer index by `times`, wrapping around if necessary.
// It also supports negative `times` to decrement the index.
func (vals *ValidatorSet) IncProposerIndex(times int64) {
	newIndex := int64(vals.proposerIndex) + times
	nVals := int64(vals.Size())

	for newIndex < 0 {
		newIndex += nVals
	}

	idx, err := tmmath.SafeConvertInt32(newIndex % nVals)
	if err != nil {
		panic(fmt.Errorf("failed to convert int64 to int32: %w", err))
	}
	vals.proposerIndex = idx
}

// Makes a copy of the validator list.
func validatorListCopy(valsList []*Validator) []*Validator {
	if len(valsList) == 0 {
		return nil
	}
	valsCopy := make([]*Validator, len(valsList))
	for i, val := range valsList {
		valsCopy[i] = val.Copy()
	}
	return valsCopy
}

// Copy each validator into a new ValidatorSet.
func (vals *ValidatorSet) Copy() *ValidatorSet {
	if vals == nil {
		return nil
	}
	valset := &ValidatorSet{
		Validators:         validatorListCopy(vals.Validators),
		ThresholdPublicKey: vals.ThresholdPublicKey,
		QuorumHash:         vals.QuorumHash,
		QuorumType:         vals.QuorumType,
		HasPublicKeys:      vals.HasPublicKeys,
		proposerIndex:      vals.proposerIndex,
	}

	return valset
}

// HasProTxHash returns true if proTxHash given is in the validator set, false -
// otherwise.
func (vals *ValidatorSet) HasProTxHash(proTxHash crypto.ProTxHash) bool {
	if len(proTxHash) == 0 {
		return false
	}
	for _, val := range vals.Validators {
		if bytes.Equal(val.ProTxHash, proTxHash) {
			return true
		}
	}
	return false
}

// GetByProTxHash returns an index of the validator with protxhash and validator
// itself (copy) if found. Otherwise, -1 and nil are returned.
func (vals *ValidatorSet) GetByProTxHash(proTxHash []byte) (index int32, val *Validator) {
	for idx, val := range vals.Validators {
		if bytes.Equal(val.ProTxHash, proTxHash) {
			index, err := tmmath.SafeConvertInt32(int64(idx))
			if err != nil {
				panic(fmt.Errorf("failed to convert int to int32: %w", err))
			}
			return index, val.Copy()
		}
	}
	return -1, nil
}

// GetByIndex returns the validator's address and validator itself (copy) by
// index.
// It returns nil values if index is less than 0 or greater or equal to
// len(ValidatorSet.Validators).
func (vals *ValidatorSet) GetByIndex(index int32) *Validator {
	if index < 0 || int(index) >= len(vals.Validators) {
		return nil
	}
	return vals.Validators[index].Copy()
}

// GetProTxHashes returns the all validator proTxHashes
func (vals *ValidatorSet) GetProTxHashes() []crypto.ProTxHash {
	proTxHashes := make([]crypto.ProTxHash, len(vals.Validators))
	for i, val := range vals.Validators {
		proTxHashes[i] = val.ProTxHash
	}
	return proTxHashes
}

// GetProTxHashesAsByteArrays returns the all validator proTxHashes as byte arrays for convenience
func (vals *ValidatorSet) GetProTxHashesAsByteArrays() [][]byte {
	proTxHashes := make([][]byte, len(vals.Validators))
	for i, val := range vals.Validators {
		proTxHashes[i] = val.ProTxHash
	}
	return proTxHashes
}

// GetPublicKeys returns the all validator publicKeys
func (vals *ValidatorSet) GetPublicKeys() []crypto.PubKey {
	publicKeys := make([]crypto.PubKey, len(vals.Validators))
	for i, val := range vals.Validators {
		publicKeys[i] = val.PubKey
	}
	return publicKeys
}

// GetProTxHashesOrdered returns the all validator proTxHashes in order
func (vals *ValidatorSet) GetProTxHashesOrdered() []crypto.ProTxHash {
	proTxHashes := make([]crypto.ProTxHash, len(vals.Validators))
	for i, val := range vals.Validators {
		proTxHashes[i] = val.ProTxHash
	}
	sort.Sort(crypto.SortProTxHash(proTxHashes))
	return proTxHashes
}

// Size returns the length of the validator set.
func (vals *ValidatorSet) Size() int {
	return len(vals.Validators)
}

// TotalVotingPower returns the sum of the voting powers of all validators.
// It recomputes the total voting power if required.
func (vals *ValidatorSet) TotalVotingPower() int64 {
	return int64(vals.Size()) * DefaultDashVotingPower
}

// QuorumVotingPower returns the voting power of the quorum if all the members existed.
func (vals *ValidatorSet) QuorumVotingPower() int64 {
	return int64(vals.QuorumTypeMemberCount()) * DefaultDashVotingPower
}

// QuorumVotingThresholdPower returns the threshold power of the voting power of the quorum if all the members existed.
// Voting is considered successful when voting power is at or above this threshold.
func (vals *ValidatorSet) QuorumVotingThresholdPower() int64 {
	return int64(vals.QuorumTypeThresholdCount()) * DefaultDashVotingPower
}

// QuorumTypeMemberCount returns a number of validators for a quorum by a type
func (vals *ValidatorSet) QuorumTypeMemberCount() int {
	size, _, err := llmq.QuorumParams(vals.QuorumType)
	if err != nil {
		return len(vals.Validators)
	}
	return size
}

// QuorumTypeThresholdCount returns a threshold number for a quorum by a type
func (vals *ValidatorSet) QuorumTypeThresholdCount() int {
	_, threshold, err := llmq.QuorumParams(vals.QuorumType)
	if err != nil {
		return len(vals.Validators)*2/3 + 1
	}
	return threshold
}

// Hash returns the Quorum Hash.
func (vals *ValidatorSet) Hash() tmbytes.HexBytes {
	if vals == nil || vals.QuorumHash == nil || vals.ThresholdPublicKey == nil {
		return []byte(nil)
	}
	bzs := make([][]byte, 2)
	bzs[0] = vals.ThresholdPublicKey.Bytes()
	bzs[1] = vals.QuorumHash
	return merkle.HashFromByteSlices(bzs)
}

// Iterate will run the given function over the set.
func (vals *ValidatorSet) Iterate(fn func(index int, val *Validator) bool) {
	for i, val := range vals.Validators {
		stop := fn(i, val.Copy())
		if stop {
			break
		}
	}
}

// Checks changes against duplicates, splits the changes in updates and
// removals, sorts them by address.
//
// Returns:
// updates, removals - the sorted lists of updates and removals
// err - non-nil if duplicate entries or entries with negative voting power are seen
//
// No changes are made to 'origChanges'.
func processChanges(origChanges []*Validator) (updates, removals []*Validator, err error) {
	// Make a deep copy of the changes and sort by proTxHash.
	changes := validatorListCopy(origChanges)
	sort.Sort(ValidatorsByProTxHashes(changes))

	removals = make([]*Validator, 0, len(changes))
	updates = make([]*Validator, 0, len(changes))
	var prevProTxHash []byte

	// Scan changes by proTxHash and append valid validators to updates or removals lists.
	for _, valUpdate := range changes {
		if bytes.Equal(valUpdate.ProTxHash, prevProTxHash) {
			err = fmt.Errorf("duplicate entry %v in %v", valUpdate, changes)
			return nil, nil, err
		}

		switch {
		case valUpdate.VotingPower < 0:
			err = fmt.Errorf("voting power can't be negative: %d", valUpdate.VotingPower)
			return nil, nil, err
		case valUpdate.VotingPower > MaxTotalVotingPower:
			err = fmt.Errorf("to prevent clipping/overflow, voting power can't be higher than %d, got %d",
				MaxTotalVotingPower, valUpdate.VotingPower)
			return nil, nil, err
		case valUpdate.VotingPower == 0:
			removals = append(removals, valUpdate)
		default:
			updates = append(updates, valUpdate)
		}

		prevProTxHash = valUpdate.ProTxHash
	}

	return updates, removals, err
}

// verifyUpdates verifies a list of updates against a validator set, making sure the allowed
// total voting power would not be exceeded if these updates would be applied to the set.
//
// Inputs:
// updates - a list of proper validator changes, i.e. they have been verified by processChanges for duplicates
//
//	and invalid values.
//
// vals - the original validator set. Note that vals is NOT modified by this function.
// removedPower - the total voting power that will be removed after the updates are verified and applied.
//
// Returns:
// tvpAfterUpdatesBeforeRemovals -  the new total voting power if these updates would be applied without the removals.
//
//	Note that this will be < 2 * MaxTotalVotingPower in case high power validators are removed and
//	validators are added/ updated with high power values.
//
// err - non-nil if the maximum allowed total voting power would be exceeded
func verifyUpdates(
	updates []*Validator,
	vals *ValidatorSet,
	removedPower int64,
) (tvpAfterUpdatesBeforeRemovals int64, err error) {

	delta := func(update *Validator, vals *ValidatorSet) int64 {
		_, val := vals.GetByProTxHash(update.ProTxHash)
		if val != nil {
			return update.VotingPower - val.VotingPower
		}
		return update.VotingPower
	}

	for _, val := range updates {
		if val.VotingPower != 0 && val.VotingPower != 100 {
			return 0, fmt.Errorf("voting power of a node can only be 0 or 100")
		}
	}

	updatesCopy := validatorListCopy(updates)
	sort.Slice(updatesCopy, func(i, j int) bool {
		return delta(updatesCopy[i], vals) < delta(updatesCopy[j], vals)
	})

	tvpAfterRemovals := vals.TotalVotingPower() - removedPower
	for _, upd := range updatesCopy {
		tvpAfterRemovals += delta(upd, vals)
		if tvpAfterRemovals > MaxTotalVotingPower {
			return 0, ErrTotalVotingPowerOverflow
		}
	}
	return tvpAfterRemovals + removedPower, nil
}

func numNewValidators(updates []*Validator, vals *ValidatorSet) int {
	numNewValidators := 0
	for _, valUpdate := range updates {
		if !vals.HasProTxHash(valUpdate.ProTxHash) {
			numNewValidators++
		}
	}
	return numNewValidators
}

// Merges the vals' validator list with the updates list.
// When two elements with same address are seen, the one from updates is selected.
// Expects updates to be a list of updates sorted by proTxHash with no duplicates or errors,
// must have been validated with verifyUpdates() and priorities computed with computeNewPriorities().
func (vals *ValidatorSet) applyUpdates(updates []*Validator) {
	existing := vals.Validators
	sort.Sort(ValidatorsByProTxHashes(existing))

	merged := make([]*Validator, len(existing)+len(updates))
	i := 0

	for len(existing) > 0 && len(updates) > 0 {
		if bytes.Compare(existing[0].ProTxHash, updates[0].ProTxHash) < 0 { // unchanged validator
			merged[i] = existing[0]
			existing = existing[1:]
		} else {
			// Apply add or update.
			merged[i] = updates[0]
			if bytes.Equal(existing[0].ProTxHash, updates[0].ProTxHash) {
				// Validator is present in both, advance existing.
				existing = existing[1:]
			}
			updates = updates[1:]
		}
		i++
	}

	// Add the elements which are left.
	for j := 0; j < len(existing); j++ {
		merged[i] = existing[j]
		i++
	}
	// OR add updates which are left.
	for j := 0; j < len(updates); j++ {
		merged[i] = updates[j]
		i++
	}

	vals.Validators = merged[:i]
}

// Checks that the validators to be removed are part of the validator set.
// No changes are made to the validator set 'vals'.
func verifyRemovals(deletes []*Validator, vals *ValidatorSet) (votingPower int64, err error) {
	removedVotingPower := int64(0)
	for _, valUpdate := range deletes {
		proTxHash := valUpdate.ProTxHash
		_, val := vals.GetByProTxHash(proTxHash)
		if val == nil {
			return removedVotingPower, fmt.Errorf("failed to find validator %X to remove", proTxHash)
		}
		removedVotingPower += val.VotingPower
	}
	if len(deletes) > len(vals.Validators) {
		panic("more deletes than validators")
	}
	return removedVotingPower, nil
}

// Removes the validators specified in 'deletes' from validator set 'vals'.
// Should not fail as verification has been done before.
// Expects vals to be sorted by address (done by applyUpdates).
func (vals *ValidatorSet) applyRemovals(deletes []*Validator) {
	existing := vals.Validators

	merged := make([]*Validator, len(existing)-len(deletes))
	i := 0

	// Loop over deletes until we removed all of them.
	for len(deletes) > 0 {
		if bytes.Equal(existing[0].ProTxHash, deletes[0].ProTxHash) {
			deletes = deletes[1:]
		} else { // Leave it in the resulting slice.
			merged[i] = existing[0]
			i++
		}
		existing = existing[1:]
	}

	// Add the elements which are left.
	for j := 0; j < len(existing); j++ {
		merged[i] = existing[j]
		i++
	}

	vals.Validators = merged[:i]
}

// Main function used by UpdateWithChangeSet() and NewValidatorSet().
// If 'allowDeletes' is false then delete operations (identified by validators with voting power 0)
// are not allowed and will trigger an error if present in 'changes'.
// The 'allowDeletes' flag is set to false by NewValidatorSet() and to true by UpdateWithChangeSet().
func (vals *ValidatorSet) updateWithChangeSet(changes []*Validator, allowDeletes bool,
	newThresholdPublicKey crypto.PubKey, newQuorumHash crypto.QuorumHash) error {
	if len(changes) == 0 {
		return nil
	}

	if newThresholdPublicKey == nil {
		return errors.New("the new threshold public key can not be nil")
	}

	if newQuorumHash == nil {
		return errors.New("the new quorum hash can not be nil")
	}

	// Check for duplicates within changes, split in 'updates' and 'deletes' lists (sorted).
	updates, deletes, err := processChanges(changes)
	if err != nil {
		return err
	}

	if !allowDeletes && len(deletes) != 0 {
		return fmt.Errorf("cannot process validators with voting power 0: %v", deletes)
	}

	// Check that the resulting set will not be empty.
	if numNewValidators(updates, vals) == 0 && len(vals.Validators) == len(deletes) {
		return errors.New("applying the validator changes would result in empty set")
	}

	// Verify that applying the 'deletes' against 'vals' will not result in error.
	// Get the voting power that is going to be removed.
	removedVotingPower, err := verifyRemovals(deletes, vals)
	if err != nil {
		return err
	}

	// Verify that applying the 'updates' against 'vals' will not result in error.
	// Get the updated total voting power before removal. Note that this is < 2 * MaxTotalVotingPower
	_, err = verifyUpdates(updates, vals, removedVotingPower)
	if err != nil {
		return err
	}
	// Apply updates and removals.
	vals.applyUpdates(updates)
	vals.applyRemovals(deletes)

	sort.Sort(ValidatorsByProTxHashes(vals.Validators))

	vals.ThresholdPublicKey = newThresholdPublicKey
	vals.QuorumHash = newQuorumHash

	return nil
}

// UpdateWithChangeSet attempts to update the validator set with 'changes'.
// It performs the following steps:
//   - validates the changes making sure there are no duplicates and splits them in updates and deletes
//   - verifies that applying the changes will not result in errors
//   - computes the total voting power BEFORE removals to ensure that in the next steps the priorities
//     across old and newly added validators are fair
//   - computes the priorities of new validators against the final set
//   - applies the updates against the validator set
//   - applies the removals against the validator set
//   - performs scaling and centering of priority values
//
// If an error is detected during verification steps, it is returned and the validator set
// is not changed.
func (vals *ValidatorSet) UpdateWithChangeSet(
	changes []*Validator,
	newThresholdPublicKey crypto.PubKey,
	newQuorumHash crypto.QuorumHash,
) error {
	return vals.updateWithChangeSet(changes, true, newThresholdPublicKey, newQuorumHash)
}

// VerifyCommit verifies +2/3 of the set had signed the given commit.
//
// It checks all the signatures! While it's safe to exit as soon as we have
// 2/3+ signatures, doing so would impact incentivization logic in the ABCI
// application that depends on the LastCommitInfo sent in BeginBlock, which
// includes which validators signed. For instance, Gaia incentivizes proposers
// with a bonus for including more than +2/3 of the signatures.
func (vals *ValidatorSet) VerifyCommit(chainID string, blockID BlockID,
	height int64, commit *Commit) error {

	// Validate Height and BlockID.
	if height != commit.Height {
		return NewErrInvalidCommitHeight(height, commit.Height)
	}
	if !blockID.Equals(commit.BlockID) {
		return fmt.Errorf("invalid commit -- wrong block ID: want %v, got %v",
			blockID, commit.BlockID)
	}

	canonVote := commit.GetCanonicalVote()
	quorumSigns, err := MakeQuorumSigns(chainID, vals.QuorumType, vals.QuorumHash, canonVote.ToProto())
	if err != nil {
		return err
	}
	if !vals.QuorumHash.Equal(commit.QuorumHash) {
		return fmt.Errorf("invalid commit -- wrong quorum hash: validator set uses %X, commit has %X",
			vals.QuorumHash, commit.QuorumHash)

	}
	err = quorumSigns.Verify(vals.ThresholdPublicKey, NewQuorumSignsFromCommit(commit))
	if err != nil {
		return fmt.Errorf("invalid commit signatures for quorum(type=%v, hash=%X), thresholdPubKey=%X: %w",
			vals.QuorumType, vals.QuorumHash, vals.ThresholdPublicKey, err)
	}
	return nil
}

//-----------------

// IsErrNotEnoughVotingPowerSigned returns true if err is
// ErrNotEnoughVotingPowerSigned.
func IsErrNotEnoughVotingPowerSigned(err error) bool {
	return errors.As(err, &ErrNotEnoughVotingPowerSigned{})
}

// ErrNotEnoughVotingPowerSigned is returned when not enough validators signed
// a commit.
type ErrNotEnoughVotingPowerSigned struct {
	Got    int64
	Needed int64
}

func (e ErrNotEnoughVotingPowerSigned) Error() string {
	return fmt.Sprintf("invalid commit -- insufficient voting power: got %d, needed more than %d", e.Got, e.Needed)
}

func (vals *ValidatorSet) ABCIEquivalentValidatorUpdates() *abci.ValidatorSetUpdate {
	var valUpdates []abci.ValidatorUpdate
	for i := 0; i < len(vals.Validators); i++ {
		valUpdate := TM2PB.NewValidatorUpdate(vals.Validators[i].PubKey, DefaultDashVotingPower,
			vals.Validators[i].ProTxHash, vals.Validators[i].NodeAddress.String())
		valUpdates = append(valUpdates, valUpdate)
	}
	abciThresholdPublicKey := cryptoenc.MustPubKeyToProto(vals.ThresholdPublicKey)
	return &abci.ValidatorSetUpdate{
		ValidatorUpdates:   valUpdates,
		ThresholdPublicKey: abciThresholdPublicKey,
		QuorumHash:         vals.QuorumHash,
	}
}

//----------------

// String returns a string representation of ValidatorSet.
//
// See StringIndented.
func (vals *ValidatorSet) String() string {
	return vals.StringIndented("")
}

// StringIndented returns an intended String.
//
// See Validator#String.
func (vals *ValidatorSet) StringIndented(indent string) string {
	if vals == nil {
		return "nil-ValidatorSet"
	}
	var valStrings []string
	vals.Iterate(func(_index int, val *Validator) bool {
		valStrings = append(valStrings, val.String())
		return false
	})

	return fmt.Sprintf(`ValidatorSet{
%s  QuorumHash: %v
%s  Validators:
%s    %v
%s}`,
		indent, vals.QuorumHash.String(),
		indent,
		indent, strings.Join(valStrings, "\n"+indent+"    "),
		indent)

}

// BasicInfoString returns a string representation of ValidatorSet without power and priority.
//
// See StringIndented.
func (vals *ValidatorSet) BasicInfoString() string {
	return vals.StringIndentedBasic("")
}

func (vals *ValidatorSet) StringIndentedBasic(indent string) string {
	if vals == nil {
		return "nil-ValidatorSet"
	}
	var valStrings []string
	vals.Iterate(func(_index int, val *Validator) bool {
		valStrings = append(valStrings, val.ShortStringBasic())
		return false
	})
	return fmt.Sprintf(`ValidatorSet{
%s  QuorumHash: %v
%s  Validators:
%s    %v
%s}`,
		indent, vals.QuorumHash.String(),
		indent,
		indent, strings.Join(valStrings, "\n"+indent+"    "),
		indent)

}

// MarshalZerologObject implements zerolog.LogObjectMarshaler
func (vals *ValidatorSet) MarshalZerologObject(e *zerolog.Event) {
	if vals == nil {
		return
	}
	e.Str("quorum_hash", vals.QuorumHash.ShortString())
	validators := zerolog.Arr()
	for _, item := range vals.Validators {
		validators.Object(item)
	}
	e.Array("validators", validators)
}

//-------------------------------------

// ValidatorsByAddress implements sort.Interface for []*Validator based on
// the Address field.
type ValidatorsByProTxHashes []*Validator

func (valz ValidatorsByProTxHashes) Len() int { return len(valz) }

func (valz ValidatorsByProTxHashes) Less(i, j int) bool {
	return bytes.Compare(valz[i].ProTxHash, valz[j].ProTxHash) == -1
}

func (valz ValidatorsByProTxHashes) Swap(i, j int) {
	valz[i], valz[j] = valz[j], valz[i]
}

// ToProto converts ValidatorSet to protobuf
func (vals *ValidatorSet) ToProto() (*tmproto.ValidatorSet, error) {
	if vals.IsNilOrEmpty() {
		return &tmproto.ValidatorSet{}, nil // validator set should never be nil
	}

	vp := new(tmproto.ValidatorSet)
	valsProto := make([]*tmproto.Validator, len(vals.Validators))
	for i := 0; i < len(vals.Validators); i++ {
		valp, err := vals.Validators[i].ToProto()
		if err != nil {
			return nil, err
		}
		valsProto[i] = valp
	}
	vp.Validators = valsProto

	valProposer, err := vals.Proposer().ToProto()
	if err != nil {
		return nil, fmt.Errorf("toProto: validatorSet proposer error: %w", err)
	}
	vp.Proposer = valProposer

	// NOTE: Sometimes we use the bytes of the proto form as a hash. This means that we need to
	// be consistent with cached data
	vp.TotalVotingPower = 0

	if vals.ThresholdPublicKey == nil {
		return nil, fmt.Errorf("thresholdPublicKey is not set")
	}

	thresholdPublicKey, err := cryptoenc.PubKeyToProto(vals.ThresholdPublicKey)
	if err != nil {
		return nil, fmt.Errorf("toProto: thresholdPublicKey error: %w", err)
	}
	vp.ThresholdPublicKey = thresholdPublicKey

	if len(vals.QuorumHash) != crypto.DefaultHashSize {
		return nil, fmt.Errorf("toProto: quorumHash is incorrect size: %d", len(vals.QuorumHash))
	}

	quorumType, err := tmmath.SafeConvertInt32(int64(vals.QuorumType))
	if err != nil {
		return nil, fmt.Errorf("toProto: quorumType error: %w", err)
	}
	vp.QuorumType = quorumType

	vp.QuorumHash = vals.QuorumHash

	vp.HasPublicKeys = vals.HasPublicKeys

	return vp, nil
}

// ValidatorSetFromProto sets a protobuf ValidatorSet to the given pointer.
// It returns an error if any of the validators from the set or the proposer
// is invalid
func ValidatorSetFromProto(vp *tmproto.ValidatorSet) (*ValidatorSet, error) {
	if vp == nil {
		return nil, ErrValidatorSetNilOrEmpty // validator set should never be nil
		// bigger issues are at play if empty
	}
	vals := new(ValidatorSet)

	vals.Validators = make([]*Validator, len(vp.Validators))
	for i := 0; i < len(vp.Validators); i++ {
		v, err := ValidatorFromProto(vp.Validators[i])
		if err != nil {
			return nil, fmt.Errorf("fromProto: validatorSet validator error: %w", err)
		}
		vals.Validators[i] = v
	}

	var err error
	proposer := vp.GetProposer()
	if proposer != nil {
		if err := vals.SetProposer(proposer.GetProTxHash()); err != nil {
			return nil, fmt.Errorf("fromProto: validatorSet proposer error: %w", err)
		}
	}

	// NOTE: We can't trust the total voting power given to us by other peers. If someone were to
	// inject a non-zeo value that wasn't the correct voting power we could assume a wrong total
	// power hence we need to recompute it.
	// FIXME: We should look to remove TotalVotingPower from proto or add it in the validators hash
	// so we don't have to do this
	vals.TotalVotingPower()

	if vp.ThresholdPublicKey.Size() > 0 {
		vals.ThresholdPublicKey, err = cryptoenc.PubKeyFromProto(vp.ThresholdPublicKey)
		if err != nil {
			return nil, fmt.Errorf("fromProto: thresholdPublicKey error: %w", err)
		}
	}

	vals.QuorumType = btcjson.LLMQType(vp.QuorumType)

	vals.QuorumHash = vp.QuorumHash

	vals.HasPublicKeys = vp.HasPublicKeys

	return vals, vals.ValidateBasic()
}

//----------------------------------------

func ValidatorUpdatesRegenerateOnProTxHashes(proTxHashes []crypto.ProTxHash) abci.ValidatorSetUpdate {
	ld := llmq.MustGenerate(proTxHashes)
	valUpdates := make([]abci.ValidatorUpdate, 0, len(proTxHashes))
	iter := ld.Iter()
	for iter.Next() {
		proTxHash, qks := iter.Value()
		valUpdate := TM2PB.NewValidatorUpdate(
			qks.PubKey,
			DefaultDashVotingPower,
			proTxHash,
			"",
		)
		valUpdates = append(valUpdates, valUpdate)
	}
	abciThresholdPublicKey := cryptoenc.MustPubKeyToProto(ld.ThresholdPubKey)
	return abci.ValidatorSetUpdate{
		ValidatorUpdates:   valUpdates,
		ThresholdPublicKey: abciThresholdPublicKey,
		QuorumHash:         crypto.RandQuorumHash(),
	}
}
