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
// any. It returns false for bytes it cannot parse.
func dropMistypedLastCommitRandomness(bz []byte) ([]byte, bool) {
	out := make([]byte, 0, len(bz))
	dropped := false
	for len(bz) > 0 {
		num, typ, n := protowire.ConsumeTag(bz)
		if n < 0 {
			return nil, false
		}
		m := protowire.ConsumeFieldValue(num, typ, bz[n:])
		if m < 0 {
			return nil, false
		}
		field := bz[:n+m]
		if num == blockLastCommitField && typ == protowire.BytesType {
			commit, _ := protowire.ConsumeBytes(bz[n:])
			cleaned, ok := dropMistypedCommitRandomness(commit)
			if ok {
				dropped = true
				field = protowire.AppendBytes(protowire.AppendTag(nil, num, typ), cleaned)
			}
		}
		out = append(out, field...)
		bz = bz[n+m:]
	}
	return out, dropped
}

// dropMistypedCommitRandomness returns the commit bytes bz without the beacon and app hash fields that
// do not have the bytes wire type, and whether there were any.
func dropMistypedCommitRandomness(bz []byte) ([]byte, bool) {
	out := make([]byte, 0, len(bz))
	dropped := false
	for len(bz) > 0 {
		num, typ, n := protowire.ConsumeTag(bz)
		if n < 0 {
			return nil, false
		}
		m := protowire.ConsumeFieldValue(num, typ, bz[n:])
		if m < 0 {
			return nil, false
		}
		if (num == commitBeaconField || num == commitAppHashField) && typ != commitRandomnessWType {
			dropped = true
		} else {
			out = append(out, bz[:n+m]...)
		}
		bz = bz[n+m:]
	}
	return out, dropped
}
