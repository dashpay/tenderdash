package mock

import (
	"encoding/binary"
	"fmt"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/libs/bytes"
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/types"
)

// NodeAddress generates a string that is accepted as validator address.
// For given `n`, the address will always be the same.
func NodeAddress(n uint64) string {
	nodeID := make([]byte, 20)
	binary.LittleEndian.PutUint64(nodeID, n)

	return fmt.Sprintf("tcp://%x@127.0.0.1:%d", nodeID, uint16(n))
}

// Validator generates a validator with only fields needed for node selection filled.
// For the same `id`, mock validator will always have the same data (proTxHash, NodeID)
func Validator(id uint64) *types.Validator {
	address, _ := p2p.ParseNodeAddress(NodeAddress(id))

	return &types.Validator{
		ProTxHash:   ProTxHash(id),
		NodeAddress: address,
	}
}

// Validators generates a slice containing `n` mock validators.
// Each element is generated using `mockValidator()`.
func Validators(n uint64) []*types.Validator {
	vals := make([]*types.Validator, 0, n)
	for i := uint64(0); i < n; i++ {
		vals = append(vals, Validator(i))
	}
	return vals
}

// ProTxHash generates a deterministic proTxHash.
// For the same `id`, generated data is always the same.
func ProTxHash(id uint64) []byte {
	data := make([]byte, crypto.ProTxHashSize)
	binary.LittleEndian.PutUint64(data, id)
	return data
}

// QuorumHash generates a deterministic quorum hash.
// For the same `id`, generated data is always the same.
func QuorumHash(id uint64) []byte {
	data := make([]byte, crypto.QuorumHashSize)
	binary.LittleEndian.PutUint64(data, id)
	return data
}

// ProTxHashes generates multiple deterministic proTxHash'es using mockProTxHash.
// Each argument will be passed to mockProTxHash.
func ProTxHashes(ids ...uint64) []bytes.HexBytes {
	hashes := make([]bytes.HexBytes, 0, len(ids))
	for _, id := range ids {
		hashes = append(hashes, ProTxHash(id))
	}
	return hashes
}

// ValidatorsProTxHashes returns slice of proTxHashes for provided list of validators
func ValidatorsProTxHashes(vals []*types.Validator) []bytes.HexBytes {
	hashes := make([]bytes.HexBytes, len(vals))
	for id, val := range vals {
		hashes[id] = val.ProTxHash
	}
	return hashes
}
