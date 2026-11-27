package p2p

import (
	"reflect"
	"testing"

	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/proto/tendermint/consensus"
)

// Every message the consensus reactor can send travels inside a p2p Envelope,
// and the router drops one the Envelope cannot carry. A consensus message type
// added without an Envelope field is therefore never delivered, whatever the
// consensus tests say, so this test fails as soon as the two unions differ.
func TestEnvelopeCarriesEveryConsensusMessage(t *testing.T) {
	wrappers := (&consensus.Message{}).XXX_OneofWrappers()
	require.NotEmpty(t, wrappers)

	for _, wrapper := range wrappers {
		field := reflect.TypeOf(wrapper).Elem().Field(0)
		msg, ok := reflect.New(field.Type.Elem()).Interface().(proto.Message)
		require.True(t, ok, "%s does not hold a proto message", field.Name)

		t.Run(field.Name, func(t *testing.T) {
			var envelope Envelope
			require.NoError(t, envelope.Wrap(msg), "the p2p Envelope cannot carry this consensus message")

			bz, err := proto.Marshal(&envelope)
			require.NoError(t, err)
			var decoded Envelope
			require.NoError(t, proto.Unmarshal(bz, &decoded))

			unwrapped, err := decoded.Unwrap()
			require.NoError(t, err)
			assert.IsType(t, msg, unwrapped)
		})
	}
}
