package math

import (
	"fmt"
	"math"
	"testing"
	"testing/quick"

	"github.com/stretchr/testify/assert"
)

func TestSafeAdd(t *testing.T) {
	f := func(a, b int64) bool {
		c, overflow := SafeAddInt64(a, b)
		return overflow != nil || c == a+b
	}
	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

func TestSafeAddClip(t *testing.T) {
	assert.EqualValues(t, math.MaxInt64, SafeAddClipInt64(math.MaxInt64, 10))
	assert.EqualValues(t, math.MaxInt64, SafeAddClipInt64(math.MaxInt64, math.MaxInt64))
	assert.EqualValues(t, math.MinInt64, SafeAddClipInt64(math.MinInt64, -10))
}

func TestSafeSubClip(t *testing.T) {
	assert.EqualValues(t, math.MinInt64, SafeSubClipInt64(math.MinInt64, 10))
	assert.EqualValues(t, 0, SafeSubClipInt64(math.MinInt64, math.MinInt64))
	assert.EqualValues(t, math.MinInt64, SafeSubClipInt64(math.MinInt64, math.MaxInt64))
	assert.EqualValues(t, math.MaxInt64, SafeSubClipInt64(math.MaxInt64, -10))
}

func TestSafeConvertUint32(t *testing.T) {
	testCases := []struct {
		a        int64
		overflow bool
	}{
		{-1, true},
		{0, false},
		{1, false},
		{math.MaxInt64, true},
		{math.MaxInt32, false},
		{math.MaxUint32, false},
		{math.MaxUint32 + 1, true},
		{math.MaxInt32, false},
	}

	for i, tc := range testCases {
		b, err := SafeConvertUint32(tc.a)
		if tc.overflow {
			assert.Error(t, err, "#%d", i)
			assert.Panics(t, func() { MustConvertUint32(tc.a) }, "#%d", i)
		} else {
			assert.EqualValues(t, tc.a, b, "#%d", i)
			assert.NotPanics(t, func() { MustConvertUint32(tc.a) }, "#%d", i)
		}

	}
}

func TestSafeMul(t *testing.T) {
	testCases := []struct {
		a        int64
		b        int64
		c        int64
		overflow bool
	}{
		0: {0, 0, 0, false},
		1: {1, 0, 0, false},
		2: {2, 3, 6, false},
		3: {2, -3, -6, false},
		4: {-2, -3, 6, false},
		5: {-2, 3, -6, false},
		6: {math.MaxInt64, 1, math.MaxInt64, false},
		7: {math.MaxInt64 / 2, 2, math.MaxInt64 - 1, false},
		8: {math.MaxInt64 / 2, 3, 0, true},
		9: {math.MaxInt64, 2, 0, true},
	}

	for i, tc := range testCases {
		c, overflow := SafeMulInt64(tc.a, tc.b)
		assert.Equal(t, tc.c, c, "#%d", i)
		assert.Equal(t, tc.overflow, overflow, "#%d", i)
	}
}

func TestSafeConvert(t *testing.T) {
	testCases := []struct {
		from interface{}
		want interface{}
		err  bool
	}{
		{int(0), int64(0), false},
		{int(math.MaxInt), int64(math.MaxInt), false},
		{int(math.MinInt), int64(math.MinInt), false},
		{uint(0), uint64(0), false},
		{uint(math.MaxUint), uint64(math.MaxUint), false},
		{int64(0), uint64(0), false},
		{int64(math.MaxInt64), uint64(math.MaxInt64), false},
		{int64(math.MinInt64), uint64(0), true},
		{uint64(math.MaxUint64), int64(0), true},
		{uint64(math.MaxInt64), int64(math.MaxInt64), false},
		{int32(-1), uint32(0), true},
		{int32(0), uint32(0), false},
		{int32(math.MaxInt32), uint32(math.MaxInt32), false},
		{int32(math.MaxInt32), int16(0), true},
		{int32(math.MinInt32), int16(0), true},
		{int32(0), int16(0), false},
		{uint32(math.MaxUint32), int32(0), true},
		{uint32(math.MaxInt32), int32(math.MaxInt32), false},
		{uint32(0), int32(0), false},
		{int16(0), uint32(0), false},
		{int16(-1), uint32(0), true},
		{int16(math.MaxInt16), uint32(math.MaxInt16), false},
		{int64(math.MinInt16), int16(math.MinInt16), false},
		{int64(math.MinInt16 - 1), int16(0), true},
		{int32(math.MinInt16), int16(math.MinInt16), false},
		{int32(math.MinInt16 - 1), int16(0), true},
		{int32(math.MinInt16 + 1), int16(math.MinInt16 + 1), false},
		{int32(math.MaxInt16), int16(math.MaxInt16), false},
		{int32(math.MaxInt16 + 1), int16(0), true},
		{int32(math.MaxInt16 - 1), int16(math.MaxInt16 - 1), false},
	}

	for i, tc := range testCases {
		testName := fmt.Sprintf("%d:%T(%d)-%T(%d)", i, tc.from, tc.from, tc.want, tc.want)
		t.Run(testName, func(t *testing.T) {
			var result interface{}
			var err error

			switch from := tc.from.(type) {
			case int:
				switch tc.want.(type) {
				case int64:
					result, err = SafeConvert[int, int64](from)
				default:
					t.Fatalf("test case %d: unsupported target type %T", i, tc.want)
				}
			case uint:
				switch tc.want.(type) {
				case uint64:
					result, err = SafeConvert[uint, uint64](from)
				default:
					t.Fatalf("test case %d: unsupported target type %T", i, tc.want)
				}
			case int64:
				switch tc.want.(type) {
				case uint64:
					result, err = SafeConvert[int64, uint64](from)
				case int64:
					result, err = SafeConvert[int64, int64](from)
				case uint16:
					result, err = SafeConvert[int64, uint16](from)
				case int16:
					result, err = SafeConvert[int64, int16](from)
				default:
					t.Fatalf("test case %d: unsupported target type %T", i, tc.want)
				}
			case uint64:
				switch tc.want.(type) {
				case int64:
					result, err = SafeConvert[uint64, int64](from)
				default:
					t.Fatalf("test case %d: unsupported target type %T", i, tc.want)
				}
			case int32:
				switch tc.want.(type) {
				case int16:
					result, err = SafeConvert[int32, int16](from)
				case uint32:
					result, err = SafeConvert[int32, uint32](from)
				default:
					t.Fatalf("test case %d: unsupported target type %T", i, tc.want)
				}
			case uint32:
				switch tc.want.(type) {
				case int16:
					result, err = SafeConvert[uint32, int16](from)
				case int32:
					result, err = SafeConvert[uint32, int32](from)
				default:
					t.Fatalf("test case %d: unsupported target type %T", i, tc.want)
				}
			case int16:
				switch tc.want.(type) {
				case int32:
					result, err = SafeConvert[int16, int32](from)
				case uint32:
					result, err = SafeConvert[int16, uint32](from)
				default:
					t.Fatalf("test case %d: unsupported target type %T", i, tc.want)
				}
			default:
				t.Fatalf("test case %d: unsupported source type %T", i, tc.from)
			}

			if (err != nil) != tc.err {
				t.Errorf("test case %d: expected error %v, got %v", i, tc.err, err)
			}
			if err == nil && result != tc.want {
				t.Errorf("test case %d: expected result %v, got %v", i, tc.want, result)
			}
		})
	}
}

func TestMustConvertPanics(t *testing.T) {
	assert.NotPanics(t, func() { MustConvert[int32, int32](0) })
	assert.Panics(t, func() { MustConvert[int32, int16](math.MaxInt16 + 1) })
	assert.NotPanics(t, func() { MustConvert[int32, int16](math.MaxInt16) })
}
