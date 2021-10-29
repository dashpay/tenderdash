package dip6

import (
	"bytes"
	"crypto/sha256"

	"github.com/tendermint/tendermint/crypto"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	"github.com/tendermint/tendermint/types"
)

// sortableValidator is a `types.Validator` which can generate SortKey(), as specified in DIP-6
type sortableValidator struct {
	*types.Validator
	quorumHash    tmbytes.HexBytes
	sortKeyCached []byte
}

// newSortableValidator extends the validator with an option to generate DIP-6 compatible SortKey()
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
