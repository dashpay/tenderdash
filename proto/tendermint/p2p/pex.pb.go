// Code generated by protoc-gen-gogo. DO NOT EDIT.
// source: tendermint/p2p/pex.proto

package p2p

import (
	fmt "fmt"
	_ "github.com/gogo/protobuf/gogoproto"
	proto "github.com/gogo/protobuf/proto"
	io "io"
	math "math"
	math_bits "math/bits"
)

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
// A compilation error at this line likely means your copy of the
// proto package needs to be updated.
const _ = proto.GoGoProtoPackageIsVersion3 // please upgrade the proto package

type PexAddress struct {
	URL string `protobuf:"bytes,1,opt,name=url,proto3" json:"url,omitempty"`
}

func (m *PexAddress) Reset()         { *m = PexAddress{} }
func (m *PexAddress) String() string { return proto.CompactTextString(m) }
func (*PexAddress) ProtoMessage()    {}
func (*PexAddress) Descriptor() ([]byte, []int) {
	return fileDescriptor_81c2f011fd13be57, []int{0}
}
func (m *PexAddress) XXX_Unmarshal(b []byte) error {
	return m.Unmarshal(b)
}
func (m *PexAddress) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	if deterministic {
		return xxx_messageInfo_PexAddress.Marshal(b, m, deterministic)
	} else {
		b = b[:cap(b)]
		n, err := m.MarshalToSizedBuffer(b)
		if err != nil {
			return nil, err
		}
		return b[:n], nil
	}
}
func (m *PexAddress) XXX_Merge(src proto.Message) {
	xxx_messageInfo_PexAddress.Merge(m, src)
}
func (m *PexAddress) XXX_Size() int {
	return m.Size()
}
func (m *PexAddress) XXX_DiscardUnknown() {
	xxx_messageInfo_PexAddress.DiscardUnknown(m)
}

var xxx_messageInfo_PexAddress proto.InternalMessageInfo

func (m *PexAddress) GetURL() string {
	if m != nil {
		return m.URL
	}
	return ""
}

type PexRequest struct {
}

func (m *PexRequest) Reset()         { *m = PexRequest{} }
func (m *PexRequest) String() string { return proto.CompactTextString(m) }
func (*PexRequest) ProtoMessage()    {}
func (*PexRequest) Descriptor() ([]byte, []int) {
	return fileDescriptor_81c2f011fd13be57, []int{1}
}
func (m *PexRequest) XXX_Unmarshal(b []byte) error {
	return m.Unmarshal(b)
}
func (m *PexRequest) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	if deterministic {
		return xxx_messageInfo_PexRequest.Marshal(b, m, deterministic)
	} else {
		b = b[:cap(b)]
		n, err := m.MarshalToSizedBuffer(b)
		if err != nil {
			return nil, err
		}
		return b[:n], nil
	}
}
func (m *PexRequest) XXX_Merge(src proto.Message) {
	xxx_messageInfo_PexRequest.Merge(m, src)
}
func (m *PexRequest) XXX_Size() int {
	return m.Size()
}
func (m *PexRequest) XXX_DiscardUnknown() {
	xxx_messageInfo_PexRequest.DiscardUnknown(m)
}

var xxx_messageInfo_PexRequest proto.InternalMessageInfo

type PexResponse struct {
	Addresses []PexAddress `protobuf:"bytes,1,rep,name=addresses,proto3" json:"addresses"`
}

func (m *PexResponse) Reset()         { *m = PexResponse{} }
func (m *PexResponse) String() string { return proto.CompactTextString(m) }
func (*PexResponse) ProtoMessage()    {}
func (*PexResponse) Descriptor() ([]byte, []int) {
	return fileDescriptor_81c2f011fd13be57, []int{2}
}
func (m *PexResponse) XXX_Unmarshal(b []byte) error {
	return m.Unmarshal(b)
}
func (m *PexResponse) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	if deterministic {
		return xxx_messageInfo_PexResponse.Marshal(b, m, deterministic)
	} else {
		b = b[:cap(b)]
		n, err := m.MarshalToSizedBuffer(b)
		if err != nil {
			return nil, err
		}
		return b[:n], nil
	}
}
func (m *PexResponse) XXX_Merge(src proto.Message) {
	xxx_messageInfo_PexResponse.Merge(m, src)
}
func (m *PexResponse) XXX_Size() int {
	return m.Size()
}
func (m *PexResponse) XXX_DiscardUnknown() {
	xxx_messageInfo_PexResponse.DiscardUnknown(m)
}

var xxx_messageInfo_PexResponse proto.InternalMessageInfo

func (m *PexResponse) GetAddresses() []PexAddress {
	if m != nil {
		return m.Addresses
	}
	return nil
}

func init() {
	proto.RegisterType((*PexAddress)(nil), "tendermint.p2p.PexAddress")
	proto.RegisterType((*PexRequest)(nil), "tendermint.p2p.PexRequest")
	proto.RegisterType((*PexResponse)(nil), "tendermint.p2p.PexResponse")
}

func init() { proto.RegisterFile("tendermint/p2p/pex.proto", fileDescriptor_81c2f011fd13be57) }

var fileDescriptor_81c2f011fd13be57 = []byte{
	// 220 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0xe2, 0x92, 0x28, 0x49, 0xcd, 0x4b,
	0x49, 0x2d, 0xca, 0xcd, 0xcc, 0x2b, 0xd1, 0x2f, 0x30, 0x2a, 0xd0, 0x2f, 0x48, 0xad, 0xd0, 0x2b,
	0x28, 0xca, 0x2f, 0xc9, 0x17, 0xe2, 0x43, 0xc8, 0xe8, 0x15, 0x18, 0x15, 0x48, 0x89, 0xa4, 0xe7,
	0xa7, 0xe7, 0x83, 0xa5, 0xf4, 0x41, 0x2c, 0x88, 0x2a, 0x25, 0x75, 0x2e, 0xae, 0x80, 0xd4, 0x0a,
	0xc7, 0x94, 0x94, 0xa2, 0xd4, 0xe2, 0x62, 0x21, 0x49, 0x2e, 0xe6, 0xd2, 0xa2, 0x1c, 0x09, 0x46,
	0x05, 0x46, 0x0d, 0x4e, 0x27, 0xf6, 0x47, 0xf7, 0xe4, 0x99, 0x43, 0x83, 0x7c, 0x82, 0x40, 0x62,
	0x4a, 0x3c, 0x60, 0x85, 0x41, 0xa9, 0x85, 0xa5, 0xa9, 0xc5, 0x25, 0x4a, 0xbe, 0x5c, 0xdc, 0x60,
	0x5e, 0x71, 0x41, 0x7e, 0x5e, 0x71, 0xaa, 0x90, 0x1d, 0x17, 0x67, 0x22, 0xc4, 0x88, 0xd4, 0x62,
	0x09, 0x46, 0x05, 0x66, 0x0d, 0x6e, 0x23, 0x29, 0x3d, 0x54, 0xfb, 0xf5, 0x10, 0xd6, 0x38, 0xb1,
	0x9c, 0xb8, 0x27, 0xcf, 0x10, 0x84, 0xd0, 0xe2, 0xe4, 0x7f, 0xe2, 0x91, 0x1c, 0xe3, 0x85, 0x47,
	0x72, 0x8c, 0x0f, 0x1e, 0xc9, 0x31, 0x4e, 0x78, 0x2c, 0xc7, 0x70, 0xe1, 0xb1, 0x1c, 0xc3, 0x8d,
	0xc7, 0x72, 0x0c, 0x51, 0xa6, 0xe9, 0x99, 0x25, 0x19, 0xa5, 0x49, 0x7a, 0xc9, 0xf9, 0xb9, 0xfa,
	0x48, 0x5e, 0x45, 0xf6, 0x35, 0xd8, 0x4b, 0xa8, 0xc1, 0x90, 0xc4, 0x06, 0x16, 0x35, 0x06, 0x04,
	0x00, 0x00, 0xff, 0xff, 0xe7, 0x5b, 0xe7, 0x33, 0x1f, 0x01, 0x00, 0x00,
}

func (m *PexAddress) Marshal() (dAtA []byte, err error) {
	size := m.Size()
	dAtA = make([]byte, size)
	n, err := m.MarshalToSizedBuffer(dAtA[:size])
	if err != nil {
		return nil, err
	}
	return dAtA[:n], nil
}

func (m *PexAddress) MarshalTo(dAtA []byte) (int, error) {
	size := m.Size()
	return m.MarshalToSizedBuffer(dAtA[:size])
}

func (m *PexAddress) MarshalToSizedBuffer(dAtA []byte) (int, error) {
	i := len(dAtA)
	_ = i
	var l int
	_ = l
	if len(m.URL) > 0 {
		i -= len(m.URL)
		copy(dAtA[i:], m.URL)
		i = encodeVarintPex(dAtA, i, uint64(len(m.URL)))
		i--
		dAtA[i] = 0xa
	}
	return len(dAtA) - i, nil
}

func (m *PexRequest) Marshal() (dAtA []byte, err error) {
	size := m.Size()
	dAtA = make([]byte, size)
	n, err := m.MarshalToSizedBuffer(dAtA[:size])
	if err != nil {
		return nil, err
	}
	return dAtA[:n], nil
}

func (m *PexRequest) MarshalTo(dAtA []byte) (int, error) {
	size := m.Size()
	return m.MarshalToSizedBuffer(dAtA[:size])
}

func (m *PexRequest) MarshalToSizedBuffer(dAtA []byte) (int, error) {
	i := len(dAtA)
	_ = i
	var l int
	_ = l
	return len(dAtA) - i, nil
}

func (m *PexResponse) Marshal() (dAtA []byte, err error) {
	size := m.Size()
	dAtA = make([]byte, size)
	n, err := m.MarshalToSizedBuffer(dAtA[:size])
	if err != nil {
		return nil, err
	}
	return dAtA[:n], nil
}

func (m *PexResponse) MarshalTo(dAtA []byte) (int, error) {
	size := m.Size()
	return m.MarshalToSizedBuffer(dAtA[:size])
}

func (m *PexResponse) MarshalToSizedBuffer(dAtA []byte) (int, error) {
	i := len(dAtA)
	_ = i
	var l int
	_ = l
	if len(m.Addresses) > 0 {
		for iNdEx := len(m.Addresses) - 1; iNdEx >= 0; iNdEx-- {
			{
				size, err := m.Addresses[iNdEx].MarshalToSizedBuffer(dAtA[:i])
				if err != nil {
					return 0, err
				}
				i -= size
				i = encodeVarintPex(dAtA, i, uint64(size))
			}
			i--
			dAtA[i] = 0xa
		}
	}
	return len(dAtA) - i, nil
}

func encodeVarintPex(dAtA []byte, offset int, v uint64) int {
	offset -= sovPex(v)
	base := offset
	for v >= 1<<7 {
		dAtA[offset] = uint8(v&0x7f | 0x80)
		v >>= 7
		offset++
	}
	dAtA[offset] = uint8(v)
	return base
}
func (m *PexAddress) Size() (n int) {
	if m == nil {
		return 0
	}
	var l int
	_ = l
	l = len(m.URL)
	if l > 0 {
		n += 1 + l + sovPex(uint64(l))
	}
	return n
}

func (m *PexRequest) Size() (n int) {
	if m == nil {
		return 0
	}
	var l int
	_ = l
	return n
}

func (m *PexResponse) Size() (n int) {
	if m == nil {
		return 0
	}
	var l int
	_ = l
	if len(m.Addresses) > 0 {
		for _, e := range m.Addresses {
			l = e.Size()
			n += 1 + l + sovPex(uint64(l))
		}
	}
	return n
}

func sovPex(x uint64) (n int) {
	return (math_bits.Len64(x|1) + 6) / 7
}
func sozPex(x uint64) (n int) {
	return sovPex(uint64((x << 1) ^ uint64((int64(x) >> 63))))
}
func (m *PexAddress) Unmarshal(dAtA []byte) error {
	l := len(dAtA)
	iNdEx := 0
	for iNdEx < l {
		preIndex := iNdEx
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return ErrIntOverflowPex
			}
			if iNdEx >= l {
				return io.ErrUnexpectedEOF
			}
			b := dAtA[iNdEx]
			iNdEx++
			wire |= uint64(b&0x7F) << shift
			if b < 0x80 {
				break
			}
		}
		fieldNum := int32(wire >> 3)
		wireType := int(wire & 0x7)
		if wireType == 4 {
			return fmt.Errorf("proto: PexAddress: wiretype end group for non-group")
		}
		if fieldNum <= 0 {
			return fmt.Errorf("proto: PexAddress: illegal tag %d (wire type %d)", fieldNum, wire)
		}
		switch fieldNum {
		case 1:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field URL", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowPex
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLen |= uint64(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLen := int(stringLen)
			if intStringLen < 0 {
				return ErrInvalidLengthPex
			}
			postIndex := iNdEx + intStringLen
			if postIndex < 0 {
				return ErrInvalidLengthPex
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.URL = string(dAtA[iNdEx:postIndex])
			iNdEx = postIndex
		default:
			iNdEx = preIndex
			skippy, err := skipPex(dAtA[iNdEx:])
			if err != nil {
				return err
			}
			if (skippy < 0) || (iNdEx+skippy) < 0 {
				return ErrInvalidLengthPex
			}
			if (iNdEx + skippy) > l {
				return io.ErrUnexpectedEOF
			}
			iNdEx += skippy
		}
	}

	if iNdEx > l {
		return io.ErrUnexpectedEOF
	}
	return nil
}
func (m *PexRequest) Unmarshal(dAtA []byte) error {
	l := len(dAtA)
	iNdEx := 0
	for iNdEx < l {
		preIndex := iNdEx
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return ErrIntOverflowPex
			}
			if iNdEx >= l {
				return io.ErrUnexpectedEOF
			}
			b := dAtA[iNdEx]
			iNdEx++
			wire |= uint64(b&0x7F) << shift
			if b < 0x80 {
				break
			}
		}
		fieldNum := int32(wire >> 3)
		wireType := int(wire & 0x7)
		if wireType == 4 {
			return fmt.Errorf("proto: PexRequest: wiretype end group for non-group")
		}
		if fieldNum <= 0 {
			return fmt.Errorf("proto: PexRequest: illegal tag %d (wire type %d)", fieldNum, wire)
		}
		switch fieldNum {
		default:
			iNdEx = preIndex
			skippy, err := skipPex(dAtA[iNdEx:])
			if err != nil {
				return err
			}
			if (skippy < 0) || (iNdEx+skippy) < 0 {
				return ErrInvalidLengthPex
			}
			if (iNdEx + skippy) > l {
				return io.ErrUnexpectedEOF
			}
			iNdEx += skippy
		}
	}

	if iNdEx > l {
		return io.ErrUnexpectedEOF
	}
	return nil
}
func (m *PexResponse) Unmarshal(dAtA []byte) error {
	l := len(dAtA)
	iNdEx := 0
	for iNdEx < l {
		preIndex := iNdEx
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return ErrIntOverflowPex
			}
			if iNdEx >= l {
				return io.ErrUnexpectedEOF
			}
			b := dAtA[iNdEx]
			iNdEx++
			wire |= uint64(b&0x7F) << shift
			if b < 0x80 {
				break
			}
		}
		fieldNum := int32(wire >> 3)
		wireType := int(wire & 0x7)
		if wireType == 4 {
			return fmt.Errorf("proto: PexResponse: wiretype end group for non-group")
		}
		if fieldNum <= 0 {
			return fmt.Errorf("proto: PexResponse: illegal tag %d (wire type %d)", fieldNum, wire)
		}
		switch fieldNum {
		case 1:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Addresses", wireType)
			}
			var msglen int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowPex
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				msglen |= int(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			if msglen < 0 {
				return ErrInvalidLengthPex
			}
			postIndex := iNdEx + msglen
			if postIndex < 0 {
				return ErrInvalidLengthPex
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.Addresses = append(m.Addresses, PexAddress{})
			if err := m.Addresses[len(m.Addresses)-1].Unmarshal(dAtA[iNdEx:postIndex]); err != nil {
				return err
			}
			iNdEx = postIndex
		default:
			iNdEx = preIndex
			skippy, err := skipPex(dAtA[iNdEx:])
			if err != nil {
				return err
			}
			if (skippy < 0) || (iNdEx+skippy) < 0 {
				return ErrInvalidLengthPex
			}
			if (iNdEx + skippy) > l {
				return io.ErrUnexpectedEOF
			}
			iNdEx += skippy
		}
	}

	if iNdEx > l {
		return io.ErrUnexpectedEOF
	}
	return nil
}
func skipPex(dAtA []byte) (n int, err error) {
	l := len(dAtA)
	iNdEx := 0
	depth := 0
	for iNdEx < l {
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return 0, ErrIntOverflowPex
			}
			if iNdEx >= l {
				return 0, io.ErrUnexpectedEOF
			}
			b := dAtA[iNdEx]
			iNdEx++
			wire |= (uint64(b) & 0x7F) << shift
			if b < 0x80 {
				break
			}
		}
		wireType := int(wire & 0x7)
		switch wireType {
		case 0:
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return 0, ErrIntOverflowPex
				}
				if iNdEx >= l {
					return 0, io.ErrUnexpectedEOF
				}
				iNdEx++
				if dAtA[iNdEx-1] < 0x80 {
					break
				}
			}
		case 1:
			iNdEx += 8
		case 2:
			var length int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return 0, ErrIntOverflowPex
				}
				if iNdEx >= l {
					return 0, io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				length |= (int(b) & 0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			if length < 0 {
				return 0, ErrInvalidLengthPex
			}
			iNdEx += length
		case 3:
			depth++
		case 4:
			if depth == 0 {
				return 0, ErrUnexpectedEndOfGroupPex
			}
			depth--
		case 5:
			iNdEx += 4
		default:
			return 0, fmt.Errorf("proto: illegal wireType %d", wireType)
		}
		if iNdEx < 0 {
			return 0, ErrInvalidLengthPex
		}
		if depth == 0 {
			return iNdEx, nil
		}
	}
	return 0, io.ErrUnexpectedEOF
}

var (
	ErrInvalidLengthPex        = fmt.Errorf("proto: negative length found during unmarshaling")
	ErrIntOverflowPex          = fmt.Errorf("proto: integer overflow")
	ErrUnexpectedEndOfGroupPex = fmt.Errorf("proto: unexpected end of group")
)
