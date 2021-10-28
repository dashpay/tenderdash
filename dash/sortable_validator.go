package dash

import (
	"bytes"
	"crypto/sha256"

	"github.com/tendermint/tendermint/crypto"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	"github.com/tendermint/tendermint/types"
)

type sortableValidator struct {
	*types.Validator
	quorumHash tmbytes.HexBytes

	sortKeyCached []byte
}

func newSortableValidator(validator *types.Validator, quorumHash tmbytes.HexBytes) sortableValidator {
	sv := sortableValidator{
		Validator:  validator,
		quorumHash: make([]byte, crypto.QuorumHashSize),
	}

	copy(sv.quorumHash, quorumHash)
	return sv
}

// Key calculates new key for this SortableValidator, which is SHA256(proTxHash, quorumHash), as per DIP-6.
// Key is  cached.
func (v sortableValidator) SortKey() []byte {
	if len(v.sortKeyCached) == 0 {
		keyBytes := make([]byte, 0, len(v.ProTxHash)+len(v.quorumHash))
		keyBytes = append(keyBytes, v.ProTxHash...)
		keyBytes = append(keyBytes, v.quorumHash...)
		keySHA := sha256.Sum256(keyBytes)
		v.sortKeyCached = keySHA[:]
	}

	return v.sortKeyCached
}

// Equal returns info if this sortable validator is equal to the other one, based on ProTxHash
func (v sortableValidator) Equal(other sortableValidator) bool {
	return bytes.Equal(v.ProTxHash, other.ProTxHash)
}

type sortableValidatorList []sortableValidator

func newSortableValidatorList(validators []*types.Validator, quorumHash tmbytes.HexBytes) sortableValidatorList {
	ret := make(sortableValidatorList, 0, len(validators))
	for _, validator := range validators {
		sv := newSortableValidator(validator, quorumHash)
		ret = append(ret, sv)
	}
	return ret
}

// Len implements sort.Interface. It returns length of sortableValidatorList
func (vl sortableValidatorList) Len() int {
	return len(vl)
}

// Less implements sort.Interface. It returns true when i'th element
// of sortableValidatorList has lower key than j'th element.
func (vl sortableValidatorList) Less(i, j int) bool {
	return (bytes.Compare(vl[i].SortKey(), vl[j].SortKey()) < 0)
}

// Swap implements sort.Interface. It swaps i'th element with j'th element.
func (vl sortableValidatorList) Swap(i, j int) {
	vl[i], vl[j] = vl[j], vl[i]
}

// Index finds a validator on the list and returns its index.
// Assumes the list is sorted. It uses sortableValidator.Equal() to compare validators.
// Returns -1 when validator was not found.
func (vl sortableValidatorList) Index(search sortableValidator) int {
	for index, validator := range vl {
		if search.Equal(validator) {
			return index
		}
	}
	return -1
}
