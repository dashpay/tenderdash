package math

import (
	"errors"
	"fmt"
	"math"
)

var ErrOverflowInt64 = errors.New("int64 overflow")
var ErrOverflowInt32 = errors.New("int32 overflow")
var ErrOverflowUint64 = errors.New("uint64 overflow")
var ErrOverflowUint32 = errors.New("uint32 overflow")
var ErrOverflowUint8 = errors.New("uint8 overflow")
var ErrOverflowInt8 = errors.New("int8 overflow")
var ErrOverflow = errors.New("integer overflow")

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
func SafeConvertInt32[T Integer](a T) (int32, error) {
	if int64(a) > math.MaxInt32 {
		return 0, ErrOverflowInt32
	} else if int64(a) < math.MinInt32 {
		return 0, ErrOverflowInt32
	}
	return int32(a), nil
}

// SafeConvertInt32 takes a int and checks if it overflows.
func SafeConvertUint32[T Integer](a T) (uint32, error) {
	if uint64(a) > math.MaxUint32 {
		return 0, ErrOverflowUint32
	} else if a < 0 {
		return 0, ErrOverflowUint32
	}
	return uint32(a), nil
}

type Integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

// SafeConvert converts a value of type T to a value of type U.
// It returns an error if the conversion would cause an overflow.
func SafeConvert[T Integer, U Integer](from T) (U, error) {
	const uintIsSmall = math.MaxUint < math.MaxUint64
	const intIsSmall = math.MaxInt < math.MaxInt64 && math.MinInt > math.MinInt64

	// special case for int64 and uint64 inputs; all other types are safe to convert to int64
	switch any(from).(type) {
	case int64:
		// conversion from int64 to uint64 - we need to check for negative values
		if _, ok := any(U(0)).(uint64); ok && from < 0 {
			return 0, ErrOverflow
		}
		return U(from), nil
	case uint64:
		// conversion from uint64 to int64 - we need to check for overflow
		if _, ok := any(U(0)).(int64); ok && uint64(from) > math.MaxInt64 {
			return 0, ErrOverflow
		}
		return U(from), nil
	case int:
		if !intIsSmall {
			return SafeConvert[int64, U](int64(from))
		}
		// no return here - it's safe to use normal logic
	case uint:
		if !uintIsSmall {
			return SafeConvert[uint64, U](uint64(from))
		}
		// no return here - it's safe to use normal logic
	}
	if uint64(from) > Max[U]() {
		return 0, ErrOverflow
	}
	if int64(from) < Min[U]() {
		return 0, ErrOverflow
	}
	return U(from), nil
}

func MustConvert[FROM Integer, TO Integer](a FROM) TO {
	i, err := SafeConvert[FROM, TO](a)
	if err != nil {
		panic(fmt.Errorf("cannot convert %d to %T: %w", a, any(i), err))
	}
	return i
}

func MustConvertUint64[T Integer](a T) uint64 {
	return MustConvert[T, uint64](a)
}

func MustConvertInt64[T Integer](a T) int64 {
	return MustConvert[T, int64](a)
}

func MustConvertUint16[T Integer](a T) uint16 {
	return MustConvert[T, uint16](a)
}

func MustConvertInt16[T Integer](a T) int16 {
	return MustConvert[T, int16](a)
}

func MustConvertUint8[T Integer](a T) uint8 {
	return MustConvert[T, uint8](a)
}

func MustConvertUint[T Integer](a T) uint {
	return MustConvert[T, uint](a)
}

func MustConvertInt[T Integer](a T) int {
	return MustConvert[T, int](a)
}

// MustConvertInt32 takes an Integer and converts it to int32.
// Panics if the conversion overflows.
func MustConvertInt32[T Integer](a T) int32 {
	i, err := SafeConvertInt32(a)
	if err != nil {
		panic(fmt.Errorf("cannot convert %d to int32: %w", a, err))
	}
	return i
}

// MustConvertInt32 takes an Integer and converts it to int32.
// Panics if the conversion overflows.
func MustConvertUint32[T Integer](a T) uint32 {
	i, err := SafeConvertUint32(a)
	if err != nil {
		panic(fmt.Errorf("cannot convert %d to int32: %w", a, err))
	}
	return i
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

// Max returns the maximum value for a type T.
func Max[T Integer]() uint64 {
	var max T
	switch any(max).(type) {
	case int:
		return uint64(math.MaxInt)
	case int8:
		return uint64(math.MaxInt8)
	case int16:
		return uint64(math.MaxInt16)
	case int32:
		return uint64(math.MaxInt32)
	case int64:
		return uint64(math.MaxInt64)
	case uint:
		return uint64(math.MaxUint)
	case uint8:
		return uint64(math.MaxUint8)
	case uint16:
		return uint64(math.MaxUint16)
	case uint32:
		return uint64(math.MaxUint32)
	case uint64:
		return uint64(math.MaxUint64)
	default:
		panic("unsupported type")
	}
}

// Min returns the minimum value for a type T.
func Min[T Integer]() int64 {
	switch any(T(0)).(type) {
	case int:
		return int64(math.MinInt)
	case int8:
		return int64(math.MinInt8)
	case int16:
		return int64(math.MinInt16)
	case int32:
		return int64(math.MinInt32)
	case int64:
		return math.MinInt64
	case uint, uint8, uint16, uint32, uint64:
		return 0
	default:
		panic("unsupported type")
	}
}
