package bytes

import (
	"math"
	"reflect"
	"testing"

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
			Data: struct {
				Field1 int64
				Field2 []byte `tmbytes:"size=12"`
			}{0x1234567890abcdef, []byte("1234567890ab")},
			expectLen:   8 + 12,
			expectError: "",
		},
		{
			Data: struct {
				Field1 int64
				Field2 []byte `tmbytes:"size=12"`
			}{0x1234567890abcdef, []byte("1234567890")},
			expectError: "size of Field2 MUST be 12 bytes, is 10",
		}, {
			Data: struct {
				Field1 int64
				Field2 []int64 `tmbytes:"size=12"`
			}{0x1234567890abcdef, []int64{math.MaxInt64}},
			expectError: "field Field2: unsupported slice type []int64",
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
			t.Logf("Marshaled: %+v", marshaled)
		})
	}
}
func TestMarshalTags(t *testing.T) {
	type testCase struct {
		Field1     []byte `tmbytes:"size=12"`
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
				assert.Equal(t, tc.expectSize[name], tags.size, name)
			}
		})
	}
}
