package math

import (
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
