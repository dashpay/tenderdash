package math

import (
	"errors"
	"fmt"
	"math"
)

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
		return 0, ErrOverflow
	} else if b < 0 && (a < math.MinInt64-b) {
		return 0, ErrOverflow
	}
	return a + b, nil
}

// SafeAddInt32 adds two int32 integers.
func SafeAddInt32(a, b int32) (int32, error) {
	if b > 0 && (a > math.MaxInt32-b) {
		return 0, ErrOverflow
	} else if b < 0 && (a < math.MinInt32-b) {
		return 0, ErrOverflow
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
		return 0, ErrOverflow
	} else if b < 0 && (a > math.MaxInt32+b) {
		return 0, ErrOverflow
	}
	return a - b, nil
}

// SafeConvertInt32 takes a int and checks if it overflows.
func SafeConvertInt32[T Integer](a T) (int32, error) {
	if int64(a) > math.MaxInt32 {
		return 0, ErrOverflow
	} else if int64(a) < math.MinInt32 {
		return 0, ErrOverflow
	}
	return int32(a), nil
}

// SafeConvertInt32 takes a int and checks if it overflows.
func SafeConvertUint32[T Integer](a T) (uint32, error) {
	if uint64(a) > math.MaxUint32 {
		return 0, ErrOverflow
	} else if a < 0 {
		return 0, ErrOverflow
	}
	return uint32(a), nil
}

// SafeConvertUint64 takes a int and checks if it overflows.
func SafeConvertUint64[T Integer](a T) (uint64, error) {
	return SafeConvert[T, uint64](a)
}

// SafeConvertInt64 takes a int and checks if it overflows.
func SafeConvertInt64[T Integer](a T) (int64, error) {
	return SafeConvert[T, int64](a)
}

// SafeConvertInt16 takes a int and checks if it overflows.
func SafeConvertInt16[T Integer](a T) (int16, error) {
	return SafeConvert[T, int16](a)
}

// SafeConvertUint16 takes a int and checks if it overflows.
func SafeConvertUint16[T Integer](a T) (uint16, error) {
	return SafeConvert[T, uint16](a)
}

type Integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

// SafeConvert converts a value of type T to a value of type U.
// It returns an error if the conversion would cause an overflow.
func SafeConvert[F Integer, T Integer](from F) (T, error) {
	// check if int and uint are smaller than int64 and uint64; we use constants here for performance reasons
	const uintIsSmall = math.MaxUint < math.MaxUint64
	const intIsSmall = math.MaxInt < math.MaxInt64 && math.MinInt > math.MinInt64

	// special case for int64 and uint64 inputs; all other types are safe to convert to int64
	switch any(from).(type) {
	case int64:
		// conversion from int64 to uint64 - we need to check for negative values
		if _, ok := any(T(0)).(uint64); ok && from < 0 {
			return 0, ErrOverflow
		}
		// return T(from), nil
	case uint64:
		// conversion from uint64 to int64 - we need to check for overflow
		if _, ok := any(T(0)).(int64); ok && uint64(from) > math.MaxInt64 {
			return 0, ErrOverflow
		}
		// return T(from), nil
	case int:
		if !intIsSmall {
			// when int isn't smaller than int64, we just fall back to int64
			return SafeConvert[int64, T](int64(from))
		}
		// no return here - it's safe to use normal logic
	case uint:
		if !uintIsSmall {
			// when uint isn't smaller than uint64, we just fall back to uint64
			return SafeConvert[uint64, T](uint64(from))
		}
		// no return here - it's safe to use normal logic
	}
	if from >= 0 && uint64(from) > Max[T]() {
		return 0, ErrOverflow
	}
	if from <= 0 && int64(from) < Min[T]() {
		return 0, ErrOverflow
	}
	return T(from), nil
}

// MustConvert converts a value of type T to a value of type U.
// It panics if the conversion would cause an overflow.
//
// See SafeConvert for non-panicking version.
func MustConvert[FROM Integer, TO Integer](a FROM) TO {
	i, err := SafeConvert[FROM, TO](a)
	if err != nil {
		var zero TO
		panic(fmt.Errorf("cannot convert %d to %T: %w", a, zero, err))
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
		return 0, ErrOverflow
	} else if a < 0 {
		return 0, ErrOverflow
	}
	return uint8(a), nil
}

// SafeConvertInt8 takes an int64 and checks if it overflows.
func SafeConvertInt8(a int64) (int8, error) {
	if a > math.MaxInt8 {
		return 0, ErrOverflow
	} else if a < math.MinInt8 {
		return 0, ErrOverflow
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
//
// The function panics if the type is not supported.
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
		panic(fmt.Sprintf("unsupported type %T", max))
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
