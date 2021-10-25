package dash

import (
	"bytes"
	"math"
	"sort"

	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	"github.com/tendermint/tendermint/types"
)

// SelectNodesDIP6 selects `num` nodes from the `nodes` list, based on algorithm
// described in DIP-6 https://github.com/dashpay/dips/blob/master/dip-0006.md
func SelectNodesDIP6(validators validatorMap, me *types.Validator, quorumHash tmbytes.HexBytes) []*types.Validator {
	// Build the deterministic list of quorum members:
	// 1. Retrieve the deterministic masternode list which is valid at quorumHeight
	// 2. Calculate SHA256(proTxHash, quorumHash) for each entry in the list
	// 3. Sort the resulting list by the calculated hashes
	sortedValidators := make(sortableValidatorList, 0, len(validators))
	for _, validator := range validators {
		sv := NewSortableValidator(validator, quorumHash)
		sortedValidators = append(sortedValidators, sv)
	}
	sort.Sort(sortedValidators)

	// Loop through the list until the member finds itself in the list. The index at which it finds itself is called i.
	myKey := NewSortableValidator(me, quorumHash).SortKey()
	i := -1.0
	for index, validator := range sortedValidators {
		if bytes.Equal(myKey, validator.SortKey()) {
			i = float64(index)
			break
		}
	}
	// 	Calculate indexes (i+2^k)%n where k is in the range 0..floor(log2(n-1))-1 and n is equal to the size of the list.
	n := float64(sortedValidators.Len())
	count := (math.Floor(math.Log2(n-1.0)) - 1.0)
	ret := make([]*types.Validator, 0, int(count))
	for k := 0.0; k <= count; k++ {
		index := int(math.Mod((i + math.Pow(2, k)), n))
		ret = append(ret, sortedValidators[index].Validator)
	}

	return ret
}
