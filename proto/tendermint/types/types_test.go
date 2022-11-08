package types

import (
	"testing"
	time "time"

	"github.com/gogo/protobuf/proto"
	gogotypes "github.com/gogo/protobuf/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/libs/rand"
)

func TestVoteSignBytes(t *testing.T) {
	const (
		height = 1
		round  = 2
	)
	h := rand.Bytes(crypto.HashSize)
	stateID := StateID{
		AppVersion:            2,
		Height:                height,
		AppHash:               h,
		CoreChainLockedHeight: 1,
		Time:                  *gogotypes.TimestampNow(),
	}
	v := Vote{
		Type:   PrecommitType,
		Height: height,
		Round:  round,
		BlockID: BlockID{
			Hash:          h,
			PartSetHeader: PartSetHeader{Total: 1, Hash: h},
			StateID:       &stateID,
		},
	}
	const chainID = "some-chain"
	sb, err := v.SignBytes(chainID)
	require.NoError(t, err)
	assert.Len(t, sb, 4+8+8+32+32+len(chainID)) // type(4) + height(8) + round(8) + blockID(32) + stateID(32)
}

func TestStateIDIsZero(t *testing.T) {
	type testCase struct {
		StateID
		expectZero bool
	}
	testCases := []testCase{
		{
			expectZero: true,
		},
		{
			StateID:    StateID{Time: *gogotypes.TimestampNow()},
			expectZero: false,
		},
	}
	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			assert.Equal(t, tc.expectZero, tc.IsZero())
		})
	}
}

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
			Time:                  *mustTimestamp(time.Date(2022, 3, 4, 5, 6, 7, 8, time.UTC)),
		},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			bz, err := tc.signBytes()
			stateID := StateID{}
			proto.Unmarshal(bz, &stateID)
			require.NoError(t, err)
			assert.Equal(t, tc, stateID)
		})
	}
}

// TestStateIDString checks if state ID is correctly converted to string
func TestStateIDString(t *testing.T) {

	stateID := StateID{
		AppVersion:            123,
		Height:                1,
		AppHash:               crypto.Checksum([]byte("apphash")),
		CoreChainLockedHeight: 2,
		Time:                  *mustTimestamp(time.Date(2022, 3, 4, 5, 6, 7, 8, time.UTC)),
	}
	assert.NoError(t, stateID.ValidateBasic())
	assert.Equal(t, "v1:h=1,cl=2,ah=106901,t=2022-03-04T05:06:07Z", stateID.String())
}

func mustTimestamp(t time.Time) *gogotypes.Timestamp {
	ts, err := gogotypes.TimestampProto(t)
	if err != nil {
		panic(err)
	}
	return ts
}
