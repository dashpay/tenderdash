package dip6_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tendermint/tendermint/dash/dip6"
	"github.com/tendermint/tendermint/dash/mock"
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
		me:         mock.Validator(0),
		quorumHash: nil,
		wantErr:    true,
	}, {
		name:       "Only me",
		validators: mock.Validators(1),
		me:         mock.Validator(0),
		quorumHash: mock.QuorumHash(0),
		wantErr:    true,
	}, {
		name:       "4 validators",
		validators: mock.Validators(4),
		me:         mock.Validator(0),
		quorumHash: mock.QuorumHash(0),
		wantErr:    true,
	}, {
		name:            "5 validators",
		validators:      mock.Validators(5),
		me:              mock.Validator(0),
		quorumHash:      mock.QuorumHash(0),
		wantProTxHashes: mock.ProTxHashes(0x01, 0x02),
		wantLen:         2,
	}, {
		name:       "5 validators, not me",
		validators: mock.Validators(5),
		me:         mock.Validator(1000),
		quorumHash: mock.QuorumHash(0),
		wantErr:    true,
	}, {
		name:            "5 validators, different quorum hash",
		validators:      mock.Validators(5),
		me:              mock.Validator(0),
		quorumHash:      mock.QuorumHash(1),
		wantProTxHashes: mock.ProTxHashes(0x01, 0x03),
		wantLen:         2,
	}, {
		name:            "8 validators",
		validators:      mock.Validators(6),
		me:              mock.Validator(0),
		quorumHash:      mock.QuorumHash(0),
		wantProTxHashes: mock.ProTxHashes(0x01, 0x02),
		wantLen:         2,
	}, {
		name:            "9 validators",
		validators:      mock.Validators(9),
		me:              mock.Validator(0),
		quorumHash:      mock.QuorumHash(0),
		wantProTxHashes: mock.ProTxHashes(0x06, 0x01, 0x02),
		wantLen:         3,
	}, {
		name:            "37 validators",
		validators:      mock.Validators(37),
		me:              mock.Validator(0),
		quorumHash:      mock.QuorumHash(0),
		wantProTxHashes: mock.ProTxHashes(0xc, 0xa, 0x15, 0x19, 0x1d),
		wantLen:         5,
	}, {
		name:            "37 validators, I am last",
		validators:      mock.Validators(37),
		me:              mock.Validator(36),
		quorumHash:      mock.QuorumHash(0),
		wantProTxHashes: mock.ProTxHashes(0x08, 0x1b, 0x22, 0x23, 0x16),
		wantLen:         5,
	}, {
		name:       "37 validators, not me",
		validators: mock.Validators(37),
		me:         mock.Validator(37),
		quorumHash: mock.QuorumHash(0),
		wantErr:    true,
	}}

	// nolint:scopelint
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := dip6.SelectValidatorsDIP6(tt.validators, tt.me, tt.quorumHash)
			if (err == nil) == tt.wantErr {
				t.Logf("unexpected error: %s", err)
				t.FailNow()
			}
			if tt.wantProTxHashes != nil {
				assert.EqualValues(t, tt.wantProTxHashes, mock.ValidatorsProTxHashes(got))
			}
			if tt.wantLen > 0 {
				assert.Len(t, got, tt.wantLen)
			}
		})
	}
}
