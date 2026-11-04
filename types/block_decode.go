package types

import (
	"github.com/cosmos/gogoproto/proto"
	"google.golang.org/protobuf/encoding/protowire"

	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

// Field numbers of tendermint.types.Block.last_commit and of tendermint.types.Commit.beacon and
// app_hash.
const (
	blockLastCommitField  protowire.Number = 4
	commitBeaconField     protowire.Number = 7
	commitAppHashField    protowire.Number = 8
	commitRandomnessWType                  = protowire.BytesType
)

// UnmarshalBlockProto decodes the bytes of a block, as joined from its parts.
//
// The beacon and app hash fields of a commit (fields 7 and 8) are unknown to releases before them,
// which skip them whatever their wire type. A block whose LastCommit carries one of them with another
// wire type is therefore valid to those releases, which can commit and store it, while strict
// decoding refuses it. Such occurrences are dropped here, so an upgraded node decodes every block an
// older one does. Blocks without them decode exactly as with proto.Unmarshal.
func UnmarshalBlockProto(bz []byte) (*tmproto.Block, error) {
	pb := new(tmproto.Block)
	err := proto.Unmarshal(bz, pb)
	if err == nil {
		return pb, nil
	}
	cleaned, dropped := dropMistypedLastCommitRandomness(bz)
	if !dropped {
		return nil, err
	}
	pb = new(tmproto.Block)
	if err := proto.Unmarshal(cleaned, pb); err != nil {
		return nil, err
	}
	return pb, nil
}

// dropMistypedLastCommitRandomness returns the block bytes bz without the occurrences of the
// LastCommit beacon and app hash fields that do not have the bytes wire type, and whether there were
// any. It splits the bytes into fields as the generated decoder does, and returns false where that
// decoder fails. It allocates nothing before the first field it drops, and then only the bytes it
// returns.
func dropMistypedLastCommitRandomness(bz []byte) ([]byte, bool) {
	var out []byte
	for i := 0; i < len(bz); {
		num, typ, n, ok := consumeGeneratedField(bz[i:])
		if !ok {
			return nil, false
		}
		field := bz[i : i+n]
		if num == blockLastCommitField && typ == protowire.BytesType {
			_, tagLen := proto.DecodeVarint(field)
			_, lenLen := proto.DecodeVarint(field[tagLen:])
			commit := field[tagLen+lenLen:]
			if kept, ok := keptCommitLength(commit); ok && kept < len(commit) {
				if out == nil {
					out = append(make([]byte, 0, len(bz)), bz[:i]...)
				}
				out = protowire.AppendTag(out, num, typ)
				out = protowire.AppendVarint(out, uint64(kept)) //nolint:gosec // a length, never negative
				out = appendKeptCommitFields(out, commit)
				i += n
				continue
			}
		}
		if out != nil {
			out = append(out, field...)
		}
		i += n
	}
	return out, out != nil
}

// keptCommitLength returns the length of the commit bytes bz without the beacon and app hash fields
// that do not have the bytes wire type, and false where the generated decoder fails.
func keptCommitLength(bz []byte) (int, bool) {
	kept := 0
	for len(bz) > 0 {
		num, typ, n, ok := consumeGeneratedField(bz)
		if !ok {
			return 0, false
		}
		if !mistypedCommitRandomness(num, typ) {
			kept += n
		}
		bz = bz[n:]
	}
	return kept, true
}

// appendKeptCommitFields appends to out the fields of the commit bytes bz that keptCommitLength
// counts.
func appendKeptCommitFields(out, bz []byte) []byte {
	for len(bz) > 0 {
		num, typ, n, ok := consumeGeneratedField(bz)
		if !ok {
			break
		}
		if !mistypedCommitRandomness(num, typ) {
			out = append(out, bz[:n]...)
		}
		bz = bz[n:]
	}
	return out
}

// mistypedCommitRandomness reports whether a commit field is a beacon or an app hash without the bytes
// wire type.
func mistypedCommitRandomness(num protowire.Number, typ protowire.Type) bool {
	return (num == commitBeaconField || num == commitAppHashField) && typ != commitRandomnessWType
}

// consumeGeneratedField returns the number, the wire type and the length, tag included, of the field at
// the start of bz, as the Unmarshal methods that gogoproto generates read a field they skip: the
// field number is the tag shifted right by 3 and truncated to 32 bits. It returns false where those
// methods fail. Varints are read with proto.DecodeVarint, which, like those methods and unlike
// protowire, drops the bits of a tenth byte past the 64th instead of failing.
//
// Releases before the beacon skip fields 7 and 8 of a commit with these rules, which accept more than
// protowire does, so the bytes must be split with the same rules to drop exactly those fields.
func consumeGeneratedField(bz []byte) (protowire.Number, protowire.Type, int, bool) {
	tag, n := proto.DecodeVarint(bz)
	if n == 0 {
		return 0, 0, 0, false
	}
	num, typ := protowire.Number(int32(tag>>3)), protowire.Type(tag&0x7) //nolint:gosec // truncated as generated code does
	if typ == protowire.EndGroupType || num <= 0 {
		return 0, 0, 0, false
	}
	n, ok := skipGeneratedField(bz)
	if !ok || n > len(bz) {
		return 0, 0, 0, false
	}
	return num, typ, n, true
}

// skipGeneratedField returns the length, tag included, of the field at the start of bz, computed as
// the skip functions that gogoproto generates compute it. The length may exceed len(bz), which the
// caller checks. A group ends at the first end-group tag that brings the nesting depth back to zero,
// whatever its field number.
func skipGeneratedField(bz []byte) (int, bool) {
	i, depth := 0, 0
	for i < len(bz) {
		tag, n := proto.DecodeVarint(bz[i:])
		if n == 0 {
			return 0, false
		}
		i += n
		switch protowire.Type(tag & 0x7) {
		case protowire.VarintType:
			_, n := proto.DecodeVarint(bz[i:])
			if n == 0 {
				return 0, false
			}
			i += n
		case protowire.Fixed64Type:
			i += 8
		case protowire.BytesType:
			length, n := proto.DecodeVarint(bz[i:])
			size := int(length) //nolint:gosec // wraps as the int length of generated code does
			if n == 0 || size < 0 {
				return 0, false
			}
			i += n + size
		case protowire.StartGroupType:
			depth++
		case protowire.EndGroupType:
			if depth == 0 {
				return 0, false
			}
			depth--
		case protowire.Fixed32Type:
			i += 4
		default:
			return 0, false
		}
		if i < 0 {
			return 0, false
		}
		if depth == 0 {
			return i, true
		}
	}
	return 0, false
}
