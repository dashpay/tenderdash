package quorum

import (
	"hash/crc64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tendermint/tendermint/dash/quorum/mock"
	"github.com/tendermint/tendermint/types"
)

func Test_validatorMap_String(t *testing.T) {
	vals := mock.NewValidators(5)

	tests := []struct {
		vm      validatorMap
		wantCrc uint64
	}{
		{
			vm:      newValidatorMap([]*types.Validator{}),
			wantCrc: 0x0,
		},
		{
			vm:      newValidatorMap(vals[:2]),
			wantCrc: 0xb7acb0958c821e5f,
		},
		{
			vm:      newValidatorMap(vals),
			wantCrc: 0x55096f67242d1a7b,
		},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := tt.vm.String()
			crcTable := crc64.MakeTable(crc64.ISO)
			assert.EqualValues(t, tt.wantCrc, crc64.Checksum([]byte(got), crcTable), got)
		})
	}
}
