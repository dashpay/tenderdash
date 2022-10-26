package types

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/libs/rand"
)

// TestStateIDSignBytes ensures that state ID is correctly encoded as bytes for signing.
func TestStateIDSignBytes(t *testing.T) {
	testCases := []StateID{
		{
			AppHash: rand.Bytes(32),
		},
		{
			AppVersion:            1234,
			Height:                1,
			AppHash:               rand.Bytes(32),
			CoreChainLockedHeight: 123,
			Time:                  time.Date(2012, 3, 4, 5, 6, 7, 8, time.UTC),
		},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			bz, err := tc.SignBytes()
			require.NoError(t, err)
			pos := 0
			// Version, first two bytes
			assert.EqualValues(t, tc.AppVersion, binary.LittleEndian.Uint64(bz[pos:pos+8]))
			pos += 8

			// Height, next 8 bytes
			assert.EqualValues(t, tc.Height, binary.LittleEndian.Uint64(bz[pos:pos+8]))
			pos += 8

			// AppHash, next 32 bytes
			assert.EqualValues(t, tc.AppHash, bz[pos:pos+32])
			pos += 32

			// CoreChainLockedHeight, 4 bytes
			assert.EqualValues(t, tc.CoreChainLockedHeight, binary.LittleEndian.Uint32(bz[pos:pos+4]))
			pos += 4

			// Time, 8 bytes
			assert.EqualValues(t, tc.Time.UnixNano(), int64(binary.LittleEndian.Uint64(bz[pos:pos+8])))
			pos += 8

			assert.Len(t, bz, pos)
		})
	}
}

// TestStateIDString checks if state ID is correctly converted to string
func TestStateIDString(t *testing.T) {
	stateID := StateID{
		AppVersion:            StateIDVersion,
		Height:                1,
		AppHash:               crypto.Checksum([]byte("apphash")),
		CoreChainLockedHeight: 2,
		Time:                  time.Date(2022, 3, 4, 5, 6, 7, 8, time.UTC),
	}
	assert.NoError(t, stateID.ValidateBasic())
	assert.Equal(t, "v1:h=1,cl=2,ah=106901,t=2022-03-04T05:06:07Z", stateID.String())
}
