package dash

import (
	"encoding/binary"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/libs/bytes"
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/types"
)

func TestSelectNodesDIP6(t *testing.T) {
	tests := []struct {
		name            string
		validators      []*types.Validator
		me              *types.Validator
		quorumHash      bytes.HexBytes
		wantProTxHashes []bytes.HexBytes
		wantLen         int
		wantErr         bool
	}{{
		name:       "No validators",
		validators: nil,
		me:         mockValidator(0),
		quorumHash: nil,
		wantErr:    true,
	}, {
		name:       "Only me",
		validators: mockValidators(1),
		me:         mockValidator(0),
		quorumHash: mockQuorumHash(0),
		wantErr:    true,
	}, {
		name:       "4 validators",
		validators: mockValidators(4),
		me:         mockValidator(0),
		quorumHash: mockQuorumHash(0),
		wantErr:    true,
	}, {
		name:            "5 validators",
		validators:      mockValidators(5),
		me:              mockValidator(0),
		quorumHash:      mockQuorumHash(0),
		wantProTxHashes: mockProTxHashes(0x01, 0x02),
		wantLen:         2,
	}, {
		name:       "5 validators, not me",
		validators: mockValidators(5),
		me:         mockValidator(1000),
		quorumHash: mockQuorumHash(0),
		wantErr:    true,
	}, {
		name:            "5 validators, different quorum hash",
		validators:      mockValidators(5),
		me:              mockValidator(0),
		quorumHash:      mockQuorumHash(1),
		wantProTxHashes: mockProTxHashes(0x01, 0x03),
		wantLen:         2,
	}, {
		name:            "8 validators",
		validators:      mockValidators(6),
		me:              mockValidator(0),
		quorumHash:      mockQuorumHash(0),
		wantProTxHashes: mockProTxHashes(0x01, 0x02),
		wantLen:         2,
	}, {
		name:            "9 validators",
		validators:      mockValidators(9),
		me:              mockValidator(0),
		quorumHash:      mockQuorumHash(0),
		wantProTxHashes: mockProTxHashes(0x06, 0x01, 0x02),
		wantLen:         3,
	}, {
		name:            "37 validators",
		validators:      mockValidators(37),
		me:              mockValidator(0),
		quorumHash:      mockQuorumHash(0),
		wantProTxHashes: mockProTxHashes(0xc, 0xa, 0x15, 0x19, 0x1d),
		wantLen:         5,
	}, {
		name:            "37 validators, I am last",
		validators:      mockValidators(37),
		me:              mockValidator(36),
		quorumHash:      mockQuorumHash(0),
		wantProTxHashes: mockProTxHashes(0x08, 0x1b, 0x22, 0x23, 0x16),
		wantLen:         5,
	}, {
		name:       "37 validators, not me",
		validators: mockValidators(37),
		me:         mockValidator(37),
		quorumHash: mockQuorumHash(0),
		wantErr:    true,
	}}

	// nolint:scopelint
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SelectValidatorsDIP6(tt.validators, tt.me, tt.quorumHash)
			if (err == nil) == tt.wantErr {
				t.Logf("unexpected error: %s", err)
				t.FailNow()
			}
			if tt.wantProTxHashes != nil {
				assert.EqualValues(t, tt.wantProTxHashes, getProTxHashes(got))
			}
			if tt.wantLen > 0 {
				assert.Len(t, got, tt.wantLen)
			}
		})
	}
}

// mockValidator generates a validator with only fields needed for node selection filled.
// For the same `id`, mock validator will always have the same data (proTxHash, NodeID)
func mockValidator(id int) *types.Validator {
	return &types.Validator{
		ProTxHash: mockProTxHash(id),
		NodeAddress: p2p.NodeAddress{
			NodeID: p2p.ID(strconv.Itoa(id)),
		},
	}
}

// mockValidators generates a slice containing `n` mock validators.
// Each element is generated using `mockValidator()`.
func mockValidators(n int) []*types.Validator {
	vals := make([]*types.Validator, 0, n)
	for i := 0; i < n; i++ {
		vals = append(vals, mockValidator(i))
	}
	return vals
}

// mockProTxHash generates a deterministic proTxHash.
// For the same `id`, generated data is always the same.
func mockProTxHash(id int) []byte {
	data := make([]byte, crypto.ProTxHashSize)
	binary.LittleEndian.PutUint64(data, uint64(id))
	return data
}

// mockQuorumHash generates a deterministic quorum hash.
// For the same `id`, generated data is always the same.
func mockQuorumHash(id int) []byte {
	data := make([]byte, crypto.QuorumHashSize)
	binary.LittleEndian.PutUint64(data, uint64(id))
	return data
}

// mockProTxHashes generates multiple deterministic proTxHash'es using mockProTxHash.
// Each argument will be passed to mockProTxHash.
func mockProTxHashes(ids ...int) []bytes.HexBytes {
	hashes := make([]bytes.HexBytes, 0, len(ids))
	for _, id := range ids {
		hashes = append(hashes, mockProTxHash(id))
	}
	return hashes
}

// getProTxHashes returns slice of proTxHashes for provided list of validators
func getProTxHashes(vals []*types.Validator) []bytes.HexBytes {
	hashes := make([]bytes.HexBytes, len(vals))
	for id, val := range vals {
		hashes[id] = val.ProTxHash
	}
	return hashes
}
