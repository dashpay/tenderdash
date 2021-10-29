package dip6

import (
	"bytes"
	"sort"

	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	"github.com/tendermint/tendermint/types"
)

// sortedValidatorList is a list of sortableValidators that are sorted by `sortableValidator.SortKey()`
type sortedValidatorList []sortableValidator

// newSortedValidatorList generates a sorted validator list containing provided `validators`.
// Sorting is executed based on sortableValidator.SortKey()
func newSortedValidatorList(validators []*types.Validator, quorumHash tmbytes.HexBytes) sortedValidatorList {
	ret := make(sortedValidatorList, 0, len(validators))
	for _, validator := range validators {
		sv := newSortableValidator(validator, quorumHash)
		ret = append(ret, sv)
	}

	ret.Sort()
	return ret
}

// Sort() sorts this sortableValidatorList
func (vl sortedValidatorList) Sort() {
	sort.Sort(vl)
}

// Len implements sort.Interface. It returns length of sortableValidatorList
func (vl sortedValidatorList) Len() int {
	return len(vl)
}

// Less implements sort.Interface. It returns true when i'th element
// of sortableValidatorList has lower key than j'th element.
func (vl sortedValidatorList) Less(i, j int) bool {
	return (bytes.Compare(vl[i].SortKey(), vl[j].SortKey()) < 0)
}

// Swap implements sort.Interface. It swaps i'th element with j'th element.
func (vl sortedValidatorList) Swap(i, j int) {
	vl[i], vl[j] = vl[j], vl[i]
}

// Index finds a validator on the list and returns its index.
// Assumes the list is sorted. It uses sortableValidator.Equal() (which uses ProTxHash) to compare validators.
// Returns -1 when validator was not found.
func (vl sortedValidatorList) Index(search sortableValidator) int {
	for index, validator := range vl {
		if search.Equal(validator) {
			return index
		}
	}
	return -1
}
