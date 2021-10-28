package dash

import (
	"testing"

	"github.com/stretchr/testify/assert"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	"github.com/tendermint/tendermint/types"
)

// Test_sortableValidatorList_Index checks if the sortableValidatorList.Index() works correctly
func Test_sortableValidatorList_Index(t *testing.T) {
	tests := []struct {
		name   string
		list   sortableValidatorList
		search sortableValidator
		want   int
	}{{
		name:   "miss",
		list:   newSortableValidatorList(mockValidators(5), mockQuorumHash(0)),
		search: newSortableValidator(mockValidator(10), mockQuorumHash(0)),
		want:   -1,
	}, {
		name:   "i=0",
		list:   newSortableValidatorList(mockValidators(500), mockQuorumHash(0)),
		search: newSortableValidator(mockValidator(0), mockQuorumHash(0)),
		want:   0,
	}, {
		name:   "i=4",
		list:   newSortableValidatorList(mockValidators(500), mockQuorumHash(2054231)),
		search: newSortableValidator(mockValidator(4), mockQuorumHash(2054231)),
		want:   4,
	}}
	// nolint:scopelint
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.list.Index(tt.search)
			if tt.want < 0 {
				assert.Less(t, got, 0)
			} else {
				assert.EqualValues(t, tt.search, tt.list[got])
			}

		})
	}
}

// Test_sortableValidator_SortKey checks if the sortableValidatorList.Index() works correctly
func Test_sortableValidator_SortKey(t *testing.T) {
	tests := []struct {
		name       string
		Validator  *types.Validator
		quorumHash tmbytes.HexBytes
		want       []byte
	}{{
		name:       "zero input",
		Validator:  mockValidator(0),
		quorumHash: mockQuorumHash(0),
		want: []byte{0xf5, 0xa5, 0xfd, 0x42, 0xd1, 0x6a, 0x20, 0x30, 0x27, 0x98, 0xef, 0x6e, 0xd3, 0x9, 0x97,
			0x9b, 0x43, 0x0, 0x3d, 0x23, 0x20, 0xd9, 0xf0, 0xe8, 0xea, 0x98, 0x31, 0xa9, 0x27, 0x59, 0xfb, 0x4b},
	}, {
		name:       "base value",
		Validator:  mockValidator(2362),
		quorumHash: mockQuorumHash(6454),
		want: []byte{0x8d, 0xde, 0xe8, 0xc6, 0xe3, 0xaf, 0xda, 0xbe, 0x48, 0xc0, 0x6e, 0x5b, 0x1, 0x10, 0xd8,
			0x57, 0xa1, 0xba, 0xba, 0xca, 0x9b, 0xa0, 0x17, 0xad, 0x27, 0x2d, 0x11, 0x70, 0x89, 0x53, 0x6f, 0xbe},
	}, {
		name:       "changed quorum hash",
		Validator:  mockValidator(2362),
		quorumHash: mockQuorumHash(8765),
		want: []byte{0xe5, 0x63, 0xb4, 0x7e, 0x1a, 0xeb, 0x98, 0x81, 0x4b, 0xe4, 0x69, 0x95, 0x79, 0xc4, 0x4b,
			0xe8, 0xea, 0x51, 0x8a, 0x9f, 0x51, 0x52, 0xa4, 0xba, 0x47, 0x80, 0x63, 0x90, 0x88, 0xde, 0xa6, 0x39},
		// want:       {f5a5fd42d16a20302798ef6ed309979b43003d2320d9f0e8ea9831a92759fb4b},
	}, {
		name:       "changed validator hash",
		Validator:  mockValidator(7812),
		quorumHash: mockQuorumHash(6454),
		want: []byte{0xdd, 0x90, 0xcc, 0x2f, 0xde, 0x12, 0xb3, 0x90, 0x1, 0x66, 0x1f, 0xcc, 0x69, 0xec, 0x8b,
			0x80, 0xc9, 0x72, 0xf2, 0x82, 0x5c, 0xf6, 0x73, 0xc1, 0x93, 0x1a, 0xd7, 0x33, 0x3f, 0x95, 0x33, 0x63},
	}}

	// nolint:scopelint
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newSortableValidator(tt.Validator, tt.quorumHash)
			got := v.SortKey()
			assert.EqualValues(t, tt.want, got)
		})
	}
}
