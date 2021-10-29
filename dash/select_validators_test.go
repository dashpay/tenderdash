package dash

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tendermint/tendermint/libs/bytes"
	"github.com/tendermint/tendermint/types"
)

func TestSelectValidatorsDIP6(t *testing.T) {
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
				assert.EqualValues(t, tt.wantProTxHashes, validatorsProTxHashes(got))
			}
			if tt.wantLen > 0 {
				assert.Len(t, got, tt.wantLen)
			}
		})
	}
}
