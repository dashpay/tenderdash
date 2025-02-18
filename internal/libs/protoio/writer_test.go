package protoio_test

import (
	"testing"

	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/libs/protoio"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func aVote(t testing.TB) *types.Vote {
	t.Helper()

	return &types.Vote{
		Type:   tmproto.SignedMsgType(byte(tmproto.PrevoteType)),
		Height: 12345,
		Round:  2,
		BlockID: types.BlockID{
			Hash: crypto.Checksum([]byte("blockID_hash")),
			PartSetHeader: types.PartSetHeader{
				Total: 1000000,
				Hash:  crypto.Checksum([]byte("blockID_part_set_header_hash")),
			},
		},
		ValidatorProTxHash: crypto.RandProTxHash(),
		ValidatorIndex:     56789,
	}
}

type excludedMarshalTo struct {
	msg proto.Message
}

func (emt *excludedMarshalTo) ProtoMessage() {}
func (emt *excludedMarshalTo) String() string {
	return emt.msg.String()
}
func (emt *excludedMarshalTo) Reset() {
	emt.msg.Reset()
}
func (emt *excludedMarshalTo) Marshal() ([]byte, error) {
	return proto.Marshal(emt.msg)
}

var _ proto.Message = (*excludedMarshalTo)(nil)

var sink interface{}

func BenchmarkMarshalDelimitedWithMarshalTo(b *testing.B) {
	msgs := []proto.Message{
		aVote(b).ToProto(),
	}
	benchmarkMarshalDelimited(b, msgs)
}

func BenchmarkMarshalDelimitedNoMarshalTo(b *testing.B) {
	msgs := []proto.Message{
		&excludedMarshalTo{aVote(b).ToProto()},
	}
	benchmarkMarshalDelimited(b, msgs)
}

func benchmarkMarshalDelimited(b *testing.B, msgs []proto.Message) {
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		for _, msg := range msgs {
			blob, err := protoio.MarshalDelimited(msg)
			require.Nil(b, err)
			sink = blob
		}
	}

	if sink == nil {
		b.Fatal("Benchmark did not run")
	}

	// Reset the sink.
	sink = (interface{})(nil)
}
