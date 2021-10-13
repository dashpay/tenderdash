package types

import (
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

// correctIPAddresses contains some valid IP addresses written as string
var correctIPAddresses []string = []string{
	"127.0.0.1",
	"2ffe::12:32:1",
	"::",
	"::ffff:127:0:0:1",
	"10.0.0.1",
}

func TestIPAddress_Parse(t *testing.T) {
	//nolint:scopelint
	for _, inputIP := range correctIPAddresses {
		t.Run(inputIP, func(t *testing.T) {
			ip, err := ParseIP(inputIP)
			assert.NoError(t, err)

			assert.EqualValues(t, inputIP, ip.String())
		})
	}
}

func TestIPAddress_MustParse(t *testing.T) {
	tests := []struct {
		ip          string
		shouldPanic bool
	}{
		{"0.0.0.0", false},
		{"::", false},
		{"1.2.3.4", false},
		{"1.2.3.4.5", true},
		{"", true},
		{"zero", true},
		{"00:ab:cd:ef", true},
		{"::ab:cd:ef", false},
		{"::ab::", true},
	}
	//nolint:scopelint
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := IPAddress{}

			if tt.shouldPanic {
				assert.Panics(t, func() { ip = mustParseIP(tt.ip) })
			} else {
				assert.NotPanics(t, func() { ip = mustParseIP(tt.ip) })
				assert.EqualValues(t, tt.ip, ip.String())
			}
		})
	}
}

func TestIPAddress_ParseStdIP(t *testing.T) {
	//nolint:scopelint
	for _, inputIP := range correctIPAddresses {
		t.Run(inputIP, func(t *testing.T) {
			stdip := net.ParseIP(inputIP)
			ip, err := ParseStdIP(stdip)
			assert.NoError(t, err)
			assert.EqualValues(t, inputIP, ip.String())
		})
	}
}

func TestIPAddress_ToIPAddr(t *testing.T) {
	//nolint:scopelint
	for _, inputIP := range correctIPAddresses {
		t.Run(inputIP, func(t *testing.T) {

			ip := mustParseIP(inputIP)
			stdIP := ip.ToIPAddr()
			assert.NotNil(t, stdIP)

			assert.EqualValues(t, inputIP, stdIP.String())
			assert.Zero(t, stdIP.Zone)

			ip, err := ParseStdIP(stdIP.IP)
			assert.NoError(t, err)
			assert.Equal(t, inputIP, ip.String())
		})
	}
}

func TestIPAddress_Copy(t *testing.T) {
	tests := []string{""}
	tests = append(tests, correctIPAddresses...)

	//nolint:scopelint
	for _, inputIP := range tests {
		t.Run(inputIP, func(t *testing.T) {

			// We want pointer here, to be able to change the value later on
			ip := new(IPAddress)
			// support "zero" address
			if inputIP != "" {
				*ip = mustParseIP(inputIP)
			}

			ip2 := ip.Copy()
			assert.NotSame(t, ip, &ip2, "%p should not be equal %p", ip, ip2)
			assert.EqualValues(t, ip.String(), ip2.String())
			assert.True(t, ip.Equal(ip2))

			*ip = mustParseIP("1.2.3.4")
			assert.False(t, ip.Equal(ip2))
		})
	}
}

func TestIPAddress_Marshal(t *testing.T) {
	tests := []struct {
		ip   string
		want [16]byte
	}{
		{"12.21.1.2", [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 12, 21, 1, 2}},
		{"0.0.0.0", [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 0, 0, 0, 0}},
		{"::", [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}},
		{"2001:4860:4860::8888", [16]byte{0x20, 0x01, 0x48, 0x60, 0x48, 0x60, 0, 0, 0, 0, 0, 0, 0, 0, 0x88, 0x88}},
	}
	//nolint:scopelint
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := IPAddress{}
			if tt.ip != "" {
				ip = mustParseIP(tt.ip)
			}

			got, err := ip.Marshal()
			assert.NoError(t, err)
			assert.EqualValues(t, tt.want[:], got)

			ip2 := IPAddress{}
			err = ip2.Unmarshal(got)
			assert.NoError(t, err)
			assert.True(t, ip2.Equal(ip))
		})
	}
}

func TestIPAddress_MarshalTo(t *testing.T) {
	tests := []struct {
		ip      string
		buf     []byte
		want    []byte
		wantErr bool
	}{
		{"12.21.1.2", make([]byte, 16), []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 255, 255, 12, 21, 1, 2}, false},
		{"0.0.0.0", make([]byte, 16), []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 0, 0, 0, 0}, false},
		{"::", make([]byte, 16), []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, false},
		{"2001:4860:4860::8888", make([]byte, 16),
			[]byte{0x20, 0x01, 0x48, 0x60, 0x48, 0x60, 0, 0, 0, 0, 0, 0, 0, 0, 0x88, 0x88}, false},
		{"::", make([]byte, 4), []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, true},
		{"0.0.0.0", make([]byte, 4), []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 0, 0, 0, 0}, true},
	}
	//nolint:scopelint
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := IPAddress{}
			if tt.ip != "" {
				ip = mustParseIP(tt.ip)
			}

			length := len(tt.buf)
			n, err := ip.MarshalTo(tt.buf)
			if !tt.wantErr {
				assert.Equal(t, length, n)
				assert.NoError(t, err)
				assert.EqualValues(t, tt.want, tt.buf)
				ip2 := IPAddress{}
				err = ip2.Unmarshal(tt.buf)
				assert.NoError(t, err)
				assert.True(t, ip2.Equal(ip))
			} else {
				assert.Equal(t, 0, n)
				assert.Error(t, err)
			}
		})
	}
}

func TestIPAddress_Size(t *testing.T) {
	for _, inputIP := range correctIPAddresses {
		t.Run(inputIP, func(t *testing.T) {
			ip := (&IPAddress{})
			assert.Equal(t, 16, ip.Size())
		})
	}
}

func TestIPAddress_MarshalJSON(t *testing.T) {
	tests := []struct {
		ip   string
		want []byte
	}{
		{"12.21.1.2", []byte("\"12.21.1.2\"")},
		{"0.0.0.0", []byte("\"0.0.0.0\"")},
		{"::", []byte("\"::\"")},
		{"2001:4860:4860::8888", []byte("\"2001:4860:4860::8888\"")},
		{"2001:4860:4860::0000:0000:8888", []byte("\"2001:4860:4860::8888\"")},
		{"", []byte("\"\"")},
	}
	//nolint:scopelint
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := IPAddress{}
			if tt.ip != "" {
				ip = mustParseIP(tt.ip)
			}

			got, err := ip.MarshalJSON()
			assert.NoError(t, err)
			assert.EqualValues(t, tt.want, got)

			ip2 := IPAddress{}
			err = ip2.UnmarshalJSON(got)
			assert.NoError(t, err)
			assert.True(t, ip2.Equal(ip))
		})
	}
}

func TestIPAddress_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		ip      string
		data    []byte
		wantErr bool
	}{
		{"12.21.1.2", []byte("\"12.21.1.2\""), false},
		{"0.0.0.0", []byte("\"0.0.0.0\""), false},
		{"::", []byte("\"::\""), false},
		{"2001:4860:4860::8888", []byte("\"2001:4860:4860::8888\""), false},
		{"", []byte("\"\""), false},
	}
	//nolint:scopelint
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := IPAddress{}

			err := ip.UnmarshalJSON(tt.data)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.ip, ip.String())
		})
	}
}

func TestIPAddress_Compare(t *testing.T) {
	tests := []struct {
		left   string
		right  string
		result int // -1 / 0 / 1
	}{
		{"10.20.30.40", "10.20.30.40", 0},
		{"10.20.30.39", "10.20.30.40", -1},
		{"10.20.30.41", "10.20.30.40", 1},
		{"20.20.30.40", "10.20.30.40", 1},
		// {"0.0.0.0", "::", 0}, -- FIXME this is NOT equal
		{"::ffff:0000:0000", "0.0.0.0", 0},
		{"::ffff:7f00:0001", "127.0.0.1", 0},
	}
	//nolint:scopelint
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s==%s?%d", tt.left, tt.right, tt.result), func(t *testing.T) {

			left := mustParseIP(tt.left)
			right := mustParseIP(tt.right)

			assert.Equal(t, tt.result, left.Compare(right), "left:%s right:%s", left.String(), right.String())
		})
	}
}

// ******* General purpose functions ******* //

// mustParseIP parses provided address.
// It will panic on error.
func mustParseIP(address string) IPAddress {
	ip, err := ParseIP(address)
	if err != nil {
		panic(err.Error())
	}
	return ip
}
