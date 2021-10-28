package dash

import (
	"bytes"
	"crypto/sha256"

	"github.com/tendermint/tendermint/crypto"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	"github.com/tendermint/tendermint/types"
)

type SortableValidator struct {
	*types.Validator
	quorumHash tmbytes.HexBytes

	sortKeyCached [32]byte
}

func NewSortableValidator(validator *types.Validator, quorumHash tmbytes.HexBytes) SortableValidator {
	sv := SortableValidator{
		Validator:  validator,
		quorumHash: make([]byte, crypto.QuorumHashSize),
	}

	copy(sv.quorumHash, quorumHash)
	return sv
}

// Key calculates new key for this SortableValidator, which is SHA256(proTxHash, quorumHash), as per DIP-6.
// Key is  cached.
func (v SortableValidator) SortKey() []byte {
	if len(v.sortKeyCached) == 0 {
		keyBytes := make([]byte, 0, len(v.ProTxHash)+len(v.quorumHash))
		keyBytes = append(keyBytes, v.ProTxHash...)
		keyBytes = append(keyBytes, v.quorumHash...)
		v.sortKeyCached = sha256.Sum256(keyBytes)
	}

	return v.sortKeyCached[:]
}

// Equals returns info if this sortable validator is equal to the other one
func (v SortableValidator) Equals(other SortableValidator) bool {
	return bytes.Equal(v.ProTxHash, other.ProTxHash)
}

type sortableValidatorList []SortableValidator

func (vl sortableValidatorList) Len() int {
	return len(vl)
}

func (vl sortableValidatorList) Less(i, j int) bool {
	return (bytes.Compare(vl[i].SortKey(), vl[j].SortKey()) < 0)
}

func (vl sortableValidatorList) Swap(i, j int) {
	vl[i], vl[j] = vl[j], vl[i]
}
