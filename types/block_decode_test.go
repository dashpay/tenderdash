package types

import (
	"testing"

	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

// TestUnmarshalBlockProtoMistypedLastCommitRandomness checks that a block whose LastCommit carries the
// beacon or app hash field with a wire type other than bytes decodes as releases before those fields
// decode it, without them, instead of failing: such a block is valid to those releases, which can
// commit it while upgraded nodes could not read it.
func TestUnmarshalBlockProtoMistypedLastCommitRandomness(t *testing.T) {
	block := MakeBlock(3, []Tx{Tx("tx")}, &Commit{
		Height:                  2,
		BlockID:                 makeBlockIDRandom(),
		ThresholdBlockSignature: make([]byte, SignatureSize),
	}, nil)
	block.ProposerProTxHash = crypto.RandProTxHash()
	block.ValidatorsHash = crypto.CRandBytes(crypto.HashSize)
	pb, err := block.ToProto()
	require.NoError(t, err)
	bz, err := pb.Marshal()
	require.NoError(t, err)

	plain, err := UnmarshalBlockProto(bz)
	require.NoError(t, err)
	require.True(t, proto.Equal(pb, plain), "a block without such fields decodes as with proto.Unmarshal")

	// Each suffix is another occurrence of last_commit (field 4), which protobuf merges into the first.
	for name, suffix := range map[string][]byte{
		"beacon as varint":    {0x22, 0x02, 0x38, 0x01},                   // field 7, wire type 0
		"app hash as fixed32": {0x22, 0x05, 0x45, 0x01, 0x02, 0x03, 0x04}, // field 8, wire type 5
		"beacon as fixed64":   {0x22, 0x09, 0x39, 1, 2, 3, 4, 5, 6, 7, 8}, // field 7, wire type 1
	} {
		t.Run(name, func(t *testing.T) {
			raw := append(append([]byte{}, bz...), suffix...)
			var strict tmproto.Block
			require.Error(t, proto.Unmarshal(raw, &strict), "strict decoding refuses it")

			decoded, err := UnmarshalBlockProto(raw)
			require.NoError(t, err)
			require.True(t, proto.Equal(pb, decoded), "decodes to the block without the mistyped field")
			got, err := BlockFromProto(decoded)
			require.NoError(t, err)
			require.Equal(t, block.Hash(), got.Hash())
		})
	}

	_, err = UnmarshalBlockProto(append(append([]byte{}, bz...), 0x22, 0x05, 0x38))
	require.Error(t, err, "truncated bytes still fail")
}
