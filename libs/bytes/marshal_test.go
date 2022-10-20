package bytes

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMarshalFixed(t *testing.T) {
	type testCase struct {
		Data        interface{}
		expectLen   int
		expectError string
	}
	testCases := []testCase{
		{
			Data:      struct{ T time.Time }{time.Now()},
			expectLen: 8,
		},
		{
			Data: struct {
				Field1 uint64
				Field2 []byte `tmbytes:"length=12"`
				Field3 uint32
			}{0x1234567890abcdef, []byte("1234567890ab"), math.MaxUint32},
			expectLen:   8 + 12 + 4,
			expectError: "",
		},
		{ // HexBytes
			Data: struct {
				Field1 uint64
				Field2 HexBytes `tmbytes:"length=12"`
			}{0x1234567890abcdef, []byte("1234567890ab")},
			expectLen:   8 + 12,
			expectError: "",
		},
		{
			Data: struct {
				Field1 uint64
				Field2 []byte `tmbytes:"length=12"`
			}{0x1234567890abcdef, []byte("1234567890")},
			expectError: "size of Field2 MUST be 12 bytes, is 10",
		}, { // marshal []uint64
			Data: struct {
				Field1 uint64
				Field2 []uint64 `tmbytes:"length=3"`
			}{0x1234567890abcdef, []uint64{math.MaxInt64, 0, math.MaxInt64}},
			expectLen: 8 + 3*8,
		},
		{
			Data: struct {
				Field1 string
			}{"tst\x00\x00"},
			expectError: "field Field1 of type string: cannot write: binary.Write: invalid type string",
		},
	}
	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			marshaled, err := MarshalFixedSize(tc.Data)
			if tc.expectError != "" {
				assert.ErrorContains(t, err, tc.expectError)
				return
			}
			assert.NoError(t, err)
			assert.Len(t, marshaled, tc.expectLen)
			// t.Logf("Marshaled: %+v", marshaled)
		})
	}
}
func TestMarshalTags(t *testing.T) {
	type testCase struct {
		Field1     []byte `tmbytes:"length=12"`
		Field2     int64
		Field3     string
		expectSize map[string]int
	}
	testCases := []testCase{
		{
			[]byte("abc"),
			123,
			"def",
			map[string]int{"Field1": 12},
		},
	}
	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			typ := reflect.TypeOf(tc)
			for i := 0; i < typ.NumField(); i++ {
				structField := typ.Field(i)
				if !structField.IsExported() {
					continue
				}
				name := structField.Name
				tags, err := getTags(structField)
				assert.NoError(t, err, structField.Name)
				assert.Equal(t, tc.expectSize[name], tags.length, name)
			}
		})
	}
}
