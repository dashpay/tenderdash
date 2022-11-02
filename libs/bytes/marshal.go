package bytes

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// MarshalFixed marshals provided struct as a fixed-size buffer.
// It processes exported struct fields in the order of their declaration.
// At this point, it only supports the following data types:
// * uint16
// * int64
// * slices
// * fixed-size arrays
//
// It also supports "tmbytes" tag with the following comma-separated attributes:
//   - size=N - provide number of elements in the slice (only for slices) to be enforced;
//     if 0 or not provided, size enforcement is responsibility of the caller
//
// Example:
//
//	Field []byte `tmbytes:"size=123"`
func MarshalFixedSize(data interface{}) ([]byte, error) {
	structure := reflect.Indirect(reflect.ValueOf(data))
	typ := structure.Type()
	out := bytes.NewBuffer(make([]byte, 0, typ.Size()))

	for i := 0; i < typ.NumField(); i++ {
		field := structure.Field(i)
		field = reflect.Indirect(field)
		fieldType := typ.Field(i)

		if !fieldType.IsExported() {
			continue
		}
		kind := field.Kind()
		if kind == reflect.Slice || kind == reflect.Map {
			if err := marshalVarSizedField(out, field, fieldType); err != nil {
				return nil, fmt.Errorf("field %s of type %s: cannot write: %w", fieldType.Name, field.Type(), err)
			}
			continue
		}
		if kind == reflect.String {
			s := []byte(field.String())
			if err := marshalVarSizedField(out, reflect.ValueOf(s), fieldType); err != nil {
				return nil, fmt.Errorf("field %s of type %s: cannot write: %w", fieldType.Name, field.Type(), err)
			}
			continue
		}

		switch v := field.Interface().(type) {
		case string:

		case time.Time:
			// A Timestamp represents a point in time independent of any time zone or calendar, represented as
			// seconds and fractions of seconds at nanosecond resolution in UTC Epoch time.
			// See (time.Time).UnixNano() for details.
			timestamp := v.UnixNano()
			if err := binary.Write(out, binary.LittleEndian, timestamp); err != nil {
				return nil, fmt.Errorf("field %s of type %s: cannot write: %w", fieldType.Name, field.Type(), err)
			}
		default:
			if err := binary.Write(out, binary.LittleEndian, field.Interface()); err != nil {
				return nil, fmt.Errorf("field %s of type %s: cannot write: %w", fieldType.Name, field.Type(), err)
			}
		}
	}

	return out.Bytes(), nil
}

// marshalVarSizedField marshals a field of a type with hard-to-determine size
func marshalVarSizedField(out io.Writer, field reflect.Value, structField reflect.StructField) error {
	// Variable-length objects MUST have size defined
	tags, err := getTags(structField)
	if err != nil {
		return err
	}
	if tags.size != 0 && tags.size != field.Len() {
		return fmt.Errorf("size of %s MUST be %d bytes, is %d",
			structField.Name, tags.size, field.Len())
	}
	if err := binary.Write(out, binary.LittleEndian, field.Interface()); err != nil {
		return fmt.Errorf("field %s of type %s: cannot write: %w", structField.Name, field.Type(), err)
	}

	return nil
}

func getTags(structField reflect.StructField) (tags, error) {
	var (
		err error
		ret tags
	)

	structTag, ok := structField.Tag.Lookup("tmbytes")
	if !ok {
		return tags{}, nil
	}

	for i, tag := range strings.Split(structTag, ",") {
		kv := strings.SplitN(tag, "=", 2)
		if len(kv) != 2 {
			return tags{}, fmt.Errorf("%s[%d]: invalid tag %s", structTag, i, tag)
		}
		key := kv[0]
		value := kv[1]

		switch key {
		case "size":
			ret.size, err = strconv.Atoi(value)
			if err != nil {
				return tags{}, fmt.Errorf("field %s tag size:\"%s\": %w", structField.Name, structTag, err)
			}
		default:
			return tags{}, fmt.Errorf("unsupported tag attribute %s=%s", key, value)
		}
	}

	return ret, nil
}

type tags struct {
	// size represents fixed number of elements in a variable-size data type like slice.
	// Examples:
	// * Field1 []byte `tmbytes:"size=123"` - means Field1 should contain exactly 123 bytes
	// * Field2 []int16 `tmbytes:"size=12"` - means Field1 should contain exactly 12 int16 elements, that is 24 bytes
	size int
}
