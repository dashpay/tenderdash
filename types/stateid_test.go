package types

import (
	"encoding/binary"
	"testing"

	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/assert"
)

func TestStateIDMarshal(t *testing.T) {
	testCases := []StateID{
		{AppHash: []byte{}},
		{1234, 1, []byte{0x3f, 0xff, 0xfa}},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {

			bz, err := proto.Marshal(&tc)
			assert.NoError(t, err)
			// Version, first two bytes
			pos := 0
			assert.EqualValues(t, tc.Version, binary.LittleEndian.Uint16(bz[pos:pos+2]))
			pos += 2
			// Height, next 8 bytes
			assert.EqualValues(t, tc.Height, binary.LittleEndian.Uint64(bz[pos:pos+8]))
			pos += 8
			// AppHash, next 32 bytes
			assert.Equal(t, tc.AppHash, bz[pos:pos+len(tc.AppHash)])
			for i := len(tc.AppHash); i < 32; i++ {
				assert.Zero(t, bz[pos+i], "AppHash element %d should be 0", i)
			}

			// newState := StateID{}
			// err = proto.Unmarshal(bz, &newState)
			// assert.NoError(t, err)

		})
	}
}
