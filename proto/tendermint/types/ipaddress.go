// Package types defines various protobuf types used internally by tendermint
package types

import (
	"encoding/json"
	"fmt"
	"net"

	"inet.af/netaddr"
)

// IPAddress represents an IP address that can be easily marshalled to 16-byte slice.
// It is focused on IPv6 addresses and IPv4 mapped to IPv6.
// It works nice as a gogo protobuf custom type.
type IPAddress netaddr.IP

// ************ Factory functions ********** //

// ParseIP parses provided IP and sets its value to itself. Supports IPv4 and IPv6.
func ParseIP(address string) (IPAddress, error) {
	ip := IPAddress{}
	addr, err := netaddr.ParseIP(address)
	if err != nil {
		return ip, err
	}

	// Convert ipv4 to ipv6 form without unmapping, as that's what we store
	if addr.Is4() {
		v6 := addr.As16()
		addr = netaddr.IPv6Raw(v6)
	}
	ip = IPAddress(addr)
	return ip, nil
}

// MustParseIP parses provided address.
// It will panic on error.
func MustParseIP(address string) IPAddress {
	ip, err := ParseIP(address)
	if err != nil {
		panic(err.Error())
	}
	return ip
}

// ParseStdIP sets IP address based on the standard library's net.IP type.
func ParseStdIP(address net.IP) (IPAddress, error) {
	var ip IPAddress
	addr, ok := netaddr.FromStdIP(address)
	if !ok {
		return ip, fmt.Errorf("cannot parse IP address %s", address.String())
	}

	ip = IPAddress(addr)
	return ip, nil
}

// ********** Some useful format conversions ********** //

// ToIPAddr returns the net.IPAddr representation of an IP.
// The returned value is always non-nil, but the IPAddr.
// IP will be nil if ip is the zero value.
// If ip contains a zone identifier, IPAddr.Zone is populated.
func (ip IPAddress) ToIPAddr() *net.IPAddr {
	return netaddr.IP(ip).IPAddr()
}

// Copy make a deep copy of the object and returns it.
// This is a convenience method to help people not worry about making
// deep copies of various types of objects.
func (ip IPAddress) Copy() IPAddress {
	return ip
}

// String returns string representation of an IP address.
func (ip IPAddress) String() string {
	netaddr := netaddr.IP(ip)
	if netaddr.IsZero() {
		return ""
	}

	return netaddr.Unmap().String()
}

// METHODS REQUIRED BY GOGO PROTOBUF

// Marshal converts this ip address to binary form (BigEndian 16-byte IPv6 address).
func (ip IPAddress) Marshal() ([]byte, error) {
	data := make([]byte, net.IPv6len)
	n, err := ip.MarshalTo(data)
	if err != nil {
		return nil, err
	}
	if n != net.IPv6len {
		return nil, fmt.Errorf("length mismatch: want %d, got %d", net.IPv6len, n)
	}

	return data, nil
}

// MarshalTo puts binary form of IP Address into 16-byte data slice.
func (ip IPAddress) MarshalTo(data []byte) (n int, err error) {
	addr := netaddr.IP(ip)
	ret := addr.As16()

	if len(data) < len(ret) {
		return 0, fmt.Errorf("IP address requires at least %d bytes, has only %d", len(ret), len(data))
	}

	return copy(data, ret[:]), nil
}

// Unmarshal parses BigEndian-encoded IPv6 address and stores result in `ip`.
// It also supports IPv4 addresses mapped to IPv6.
func (ip *IPAddress) Unmarshal(data []byte) error {
	if len(data) != net.IPv6len {
		return fmt.Errorf("invalid length of data to unmarshal: have %d, want %d", len(data), net.IPv6len)
	}

	var ipaddr [16]byte
	copy(ipaddr[:], data)
	addr := netaddr.IPv6Raw(ipaddr)
	*ip = IPAddress(addr)
	return nil
}

// Size returns expected size of marshalled content.
func (ip *IPAddress) Size() int {
	return net.IPv6len
}

// MarshalJSON converts this ip address to a JSON representation
func (ip IPAddress) MarshalJSON() ([]byte, error) {
	addr := netaddr.IP(ip)
	if !addr.IsValid() {
		return json.Marshal("")
	}
	return json.Marshal(ip.String())
}

// UnmarshalJSON parses JSON value of and IP address and assigns parsed value to self.
func (ip *IPAddress) UnmarshalJSON(data []byte) error {
	var (
		s    string
		addr IPAddress
	)

	err := json.Unmarshal(data, &s)
	if err != nil {
		return err
	}

	if len(s) > 0 {
		addr, err = ParseIP(s)
		if err != nil {
			return err
		}
		*ip = addr
	} else {
		// empty JSON string means zero value
		*ip = IPAddress{}
	}

	return nil
}

// Compare returns an integer comparing two IPs.
// The result will be 0 if ip==ip2, -1 if ip < ip2, and +1 if ip > ip2.
// The definition of "less than" is the same as the IP.Less method.
func (ip IPAddress) Compare(other IPAddress) int {
	return netaddr.IP(ip).Compare(netaddr.IP(other))
}

// Equal returns true if both addresses are equal, false otherwise
func (ip IPAddress) Equal(other IPAddress) bool {
	return (ip.Compare(other) == 0)
}
