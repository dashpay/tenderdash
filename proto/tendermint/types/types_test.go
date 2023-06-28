package types

import (
	"bytes"
	"encoding/hex"
	"strconv"
	"testing"
	time "time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tendermint/tendermint/internal/libs/protoio"
	"github.com/tendermint/tendermint/libs/rand"
	tmtime "github.com/tendermint/tendermint/libs/time"
)

// TestVoteSignBytes checks if sign bytes are generated correctly.
//
// This test is synchronized with tests in github.com/dashpay/rs-tenderdash-abci
func TestVoteSignBytes(t *testing.T) {
	const (
		height  = 1
		round   = 2
		chainID = "some-chain"
	)
	ts := uint64(0)
	h := bytes.Repeat([]byte{1, 2, 3, 4}, 8)

	type testCase struct {
		stateID   StateID
		vote      Vote
		expectHex string
	}

	testCases := []testCase{
		0: {
			stateID: StateID{
				AppVersion:            2,
				Height:                height,
				AppHash:               h,
				CoreChainLockedHeight: 1,
				Time:                  ts,
			},
			vote: Vote{
				Type:   PrevoteType,
				Height: height,
				Round:  round,
				BlockID: BlockID{
					Hash:          h,
					PartSetHeader: PartSetHeader{Total: 1, Hash: h},
					StateID:       []byte{}, // filled later
				},
			},
			expectHex: "0100000001000000000000000200000000000000" +
				"fb7c89bf010a91d50f890455582b7fed0c346e53" +
				"ab33df7da0bcd85c10fa92ea1854710e3fa0f558" +
				"fee16269c62154c5ca00114f96d782df690a3f6d" +
				"8e25c117736f6d652d636861696e"},
		1: {
			stateID: StateID{
				AppVersion:            2,
				Height:                height,
				AppHash:               h,
				CoreChainLockedHeight: 1,
				Time:                  ts,
			},
			vote: Vote{
				Type:   PrecommitType,
				Height: height,
				Round:  round,
				BlockID: BlockID{
					Hash:          h,
					PartSetHeader: PartSetHeader{Total: 1, Hash: h},
					StateID:       []byte{}, // filled later
				},
			},
			expectHex: "0200000001000000000000000200000000000000fb7c89bf010a91d50f890455582b7fed0c34" +
				"6e53ab33df7da0bcd85c10fa92ea1854710e3fa0f558fee16269c62154c5ca00114f96d782df690a3f6d8e2" +
				"5c117736f6d652d636861696e",
		},
	}

	for i, tc := range testCases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			vote := tc.vote
			vote.BlockID.StateID = tc.stateID.Hash()
			expected, err := hex.DecodeString(tc.expectHex)
			require.NoError(t, err)

			sb, err := vote.SignBytes(chainID)
			require.NoError(t, err)
			assert.Len(t, sb, 4+8+8+32+32+len(chainID)) // type(4) + height(8) + round(8) + blockID(32) + stateID(32)
			assert.EqualValues(t, expected, sb)

			t.Logf("state ID hash: %x sign bytes: %x", vote.BlockID.StateID, sb)
		})
	}
}

func TestStateID_Equals(t *testing.T) {
	ts := mustTimestamp(time.Date(2019, 1, 2, 3, 4, 5, 6, time.UTC))
	tests := []struct {
		state1 StateID
		state2 StateID
		equal  bool
	}{
		{
			StateID{
				AppVersion:            12,
				Height:                123,
				AppHash:               []byte("12345678901234567890123456789012"),
				CoreChainLockedHeight: 12,
				Time:                  ts,
			},
			StateID{
				AppVersion:            12,
				Height:                123,
				AppHash:               []byte("12345678901234567890123456789012"),
				CoreChainLockedHeight: 12,
				Time:                  ts,
			},
			true,
		},
		{
			StateID{
				AppVersion:            12,
				Height:                123,
				AppHash:               []byte("12345678901234567890123456789012"),
				CoreChainLockedHeight: 12,
				Time:                  ts,
			},
			StateID{
				AppVersion:            12,
				Height:                124,
				AppHash:               []byte("12345678901234567890123456789012"),
				CoreChainLockedHeight: 12,
				Time:                  ts,
			},
			false,
		},
		{
			StateID{
				AppVersion:            12,
				Height:                123,
				AppHash:               []byte("12345678901234567890123456789012"),
				CoreChainLockedHeight: 12,
				Time:                  ts,
			},
			StateID{
				AppVersion:            12,
				Height:                123,
				AppHash:               []byte("12345678901234567890123456789021"),
				CoreChainLockedHeight: 12,
				Time:                  ts,
			},
			false,
		},
	}
	//nolint:scopelint
	for tcID, tc := range tests {
		t.Run(strconv.Itoa(tcID), func(t *testing.T) {
			assert.Equal(t, tc.equal, tc.state1.Equal(tc.state2))
		})
	}
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
			StateID:    StateID{Time: uint64(tmtime.Now().UnixMilli())},
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
			Time:                  mustTimestamp(time.Date(2022, 3, 4, 5, 6, 7, 8, time.UTC)),
		},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			bz, err := tc.signBytes()
			require.NoError(t, err)
			stateID := StateID{}
			err = protoio.UnmarshalDelimited(bz, &stateID)
			require.NoError(t, err)
			assert.Equal(t, tc, stateID)
		})
	}
}

func mustTimestamp(t time.Time) uint64 {
	return uint64(t.UnixMilli())
}
