package bytes

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// MarshalFixed marshals provided struct as a fixed-size buffer.
// It processes exported struct fields in the order of their declaration.
// At this point, it only supports the following data types:
// * uint16
// * int64
// * byte slice
// It also supports "tmbytes" tag with the following comma-separated attributes:
// size=N - provide size of slice (only for slices)
// Example:
//    Field []byte `tmbytes:"size=123"`

func MarshalFixedSize(data interface{}) ([]byte, error) {
	structure := reflect.Indirect(reflect.ValueOf(data))
	typ := structure.Type()
	out := bytes.NewBuffer(make([]byte, 0, typ.Size()))

	for i := 0; i < typ.NumField(); i++ {
		var encoded []byte
		var typeOfBytes = reflect.TypeOf([]byte(nil))

		field := structure.Field(i)
		fieldType := typ.Field(i)
		kind := field.Kind()

		if !fieldType.IsExported() {
			continue
		}

		switch kind {
		case reflect.Uint16:
			encoded = binary.LittleEndian.AppendUint16([]byte{}, uint16(field.Uint()))
		case reflect.Int64:
			encoded = binary.LittleEndian.AppendUint64([]byte{}, uint64(field.Int()))
		case reflect.Slice:
			if field.Type() != typeOfBytes {
				return nil, fmt.Errorf("field %s: unsupported slice type %s", fieldType.Name, field.Type().String())
			}
			tags, err := getTags(fieldType)
			if err != nil {
				return nil, err
			}
			encoded = field.Bytes()
			if len(encoded) != tags.size {
				return nil, fmt.Errorf("size of %s MUST be %d bytes, is %d", fieldType.Name, tags.size, len(encoded))
			}
		default:
			return nil, fmt.Errorf("unsupported kind %s", kind)
		}
		out.Write(encoded)
	}

	return out.Bytes(), nil
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
	size int // size=N
}
