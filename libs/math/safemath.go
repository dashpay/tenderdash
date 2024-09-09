package math

import (
	"errors"
	"math"
)

var ErrOverflowInt64 = errors.New("int64 overflow")
var ErrOverflowInt32 = errors.New("int32 overflow")
var ErrOverflowUint8 = errors.New("uint8 overflow")
var ErrOverflowInt8 = errors.New("int8 overflow")

// SafeAddClipInt64 adds two int64 integers and clips the result to the int64 range.
func SafeAddClipInt64(a, b int64) int64 {
	c, err := SafeAddInt64(a, b)
	if err != nil {
		if b < 0 {
			return math.MinInt64
		}
		return math.MaxInt64
	}
	return c
}

// SafeAddInt64 adds two int64 integers.
func SafeAddInt64(a, b int64) (int64, error) {
	if b > 0 && (a > math.MaxInt64-b) {
		return 0, ErrOverflowInt64
	} else if b < 0 && (a < math.MinInt64-b) {
		return 0, ErrOverflowInt64
	}
	return a + b, nil
}

// SafeAddInt32 adds two int32 integers.
func SafeAddInt32(a, b int32) (int32, error) {
	if b > 0 && (a > math.MaxInt32-b) {
		return 0, ErrOverflowInt32
	} else if b < 0 && (a < math.MinInt32-b) {
		return 0, ErrOverflowInt32
	}
	return a + b, nil
}

func SafeSubInt64(a, b int64) (int64, bool) {
	if b > 0 && a < math.MinInt64+b {
		return -1, true
	} else if b < 0 && a > math.MaxInt64+b {
		return -1, true
	}
	return a - b, false
}

func SafeSubClipInt64(a, b int64) int64 {
	c, overflow := SafeSubInt64(a, b)
	if overflow {
		if b > 0 {
			return math.MinInt64
		}
		return math.MaxInt64
	}
	return c
}

// SafeSubInt32 subtracts two int32 integers.
func SafeSubInt32(a, b int32) (int32, error) {
	if b > 0 && (a < math.MinInt32+b) {
		return 0, ErrOverflowInt32
	} else if b < 0 && (a > math.MaxInt32+b) {
		return 0, ErrOverflowInt32
	}
	return a - b, nil
}

// SafeConvertInt32 takes a int and checks if it overflows.
func SafeConvertInt32(a int64) (int32, error) {
	if a > math.MaxInt32 {
		return 0, ErrOverflowInt32
	} else if a < math.MinInt32 {
		return 0, ErrOverflowInt32
	}
	return int32(a), nil
}

// SafeConvertUint8 takes an int64 and checks if it overflows.
func SafeConvertUint8(a int64) (uint8, error) {
	if a > math.MaxUint8 {
		return 0, ErrOverflowUint8
	} else if a < 0 {
		return 0, ErrOverflowUint8
	}
	return uint8(a), nil
}

// SafeConvertInt8 takes an int64 and checks if it overflows.
func SafeConvertInt8(a int64) (int8, error) {
	if a > math.MaxInt8 {
		return 0, ErrOverflowInt8
	} else if a < math.MinInt8 {
		return 0, ErrOverflowInt8
	}
	return int8(a), nil
}

func SafeMulInt64(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, false
	}

	absOfB := b
	if b < 0 {
		absOfB = -b
	}

	absOfA := a
	if a < 0 {
		absOfA = -a
	}

	if absOfA > math.MaxInt64/absOfB {
		return 0, true
	}

	return a * b, false
}
