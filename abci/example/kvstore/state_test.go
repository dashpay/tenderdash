package kvstore

import (
	"bytes"
	"testing"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
)

func TestStateMarshalUnmarshal(t *testing.T) {
	var (
		// key3 is some key with random bytes that is not printable
		key3 = tmbytes.MustHexDecode("69e57b53d00c4516a07976cb8d38a430")
		// value3 is some value with random bytes that is not printable
		value3 = tmbytes.MustHexDecode("78a8f0f46a0d75cfdad3c753aac92dc9")
	)

	state := NewKvState(dbm.NewMemDB(), 1)
	assert.NoError(t, state.Set([]byte("key1"), []byte("value1")))
	assert.NoError(t, state.Set([]byte("key2"), []byte("value2")))
	assert.NoError(t, state.Set(key3, value3))
	assert.NoError(t, state.UpdateAppHash(state, nil, nil))
	apphash := state.GetAppHash()

	encoded := bytes.NewBuffer(nil)
	err := state.Save(encoded)
	require.NoError(t, err)
	assert.NotEmpty(t, encoded)

	t.Log("encoded:", encoded.String())

	decoded := NewKvState(dbm.NewMemDB(), 1)
	err = decoded.Load(encoded)
	require.NoError(t, err)
	decoded.Print()

	v1, err := decoded.Get([]byte("key1"))
	require.NoError(t, err)
	assert.EqualValues(t, []byte("value1"), v1)

	v2, err := decoded.Get([]byte("key2"))
	require.NoError(t, err)
	assert.EqualValues(t, []byte("value2"), v2)

	v3, err := decoded.Get(key3)
	require.NoError(t, err)
	assert.EqualValues(t, value3, v3)

	assert.EqualValues(t, apphash, decoded.GetAppHash())
}

func TestStateLoad(t *testing.T) {
	const initialHeight = 12345678
	zeroAppHash := make(tmbytes.HexBytes, crypto.DefaultAppHashSize)
	type keyVals struct {
		key   []byte
		value []byte
	}
	type testCase struct {
		name              string
		encoded           []byte
		expectHeight      int64
		expectAppHash     tmbytes.HexBytes
		expectKeyVals     []keyVals
		expectDecodeError bool
	}

	testCases := []testCase{
		{
			name:              "zero json",
			encoded:           []byte{},
			expectDecodeError: true,
		},
		{
			name:          "empty json",
			encoded:       []byte{'{', '}'},
			expectHeight:  0,
			expectAppHash: zeroAppHash,
		},
		{
			name:          "only height",
			encoded:       []byte(`{"height":1234}`),
			expectHeight:  1234,
			expectAppHash: zeroAppHash,
		},
		{
			name: "full",
			encoded: []byte(`{
				"height": 6531,
				"app_hash": "1C9ECEC90E28D2461650418635878A5C91E49F47586ECF75F2B0CBB94E897112"
			}
			{"key":"key1","value":"value1"}
			{"key":"key2","value":"value2"}`),
			expectHeight:  6531,
			expectAppHash: tmbytes.MustHexDecode("1C9ECEC90E28D2461650418635878A5C91E49F47586ECF75F2B0CBB94E897112"),
			expectKeyVals: []keyVals{
				{[]byte("key1"), []byte("value1")},
				{[]byte("key2"), []byte("value2")},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			decoded := NewKvState(dbm.NewMemDB(), initialHeight)
			err := decoded.Load(bytes.NewBuffer(tc.encoded))
			if tc.expectDecodeError {
				require.Error(t, err, "decode error expected")
			} else {
				require.NoError(t, err, "decode error not expected")
			}

			if tc.expectHeight != 0 {
				assert.EqualValues(t, tc.expectHeight, decoded.GetHeight())
			}
			if tc.expectAppHash != nil {
				assert.EqualValues(t, tc.expectAppHash, decoded.GetAppHash())
			}
			for _, item := range tc.expectKeyVals {
				value, err := decoded.Get(item.key)
				assert.NoError(t, err)
				assert.EqualValues(t, item.value, value)
			}
		})
	}

}
