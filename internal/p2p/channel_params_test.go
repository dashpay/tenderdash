package p2p

import (
	"reflect"
	"testing"

	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/proto/tendermint/consensus"
)

// A generic client (p2pclient.Client.Send) picks the channel of an envelope
// that names none with ResolveChannelID, which panics on a message type it does
// not know. This fails as soon as a consensus message type is added without a
// channel.
func TestResolveChannelIDKnowsEveryConsensusMessage(t *testing.T) {
	consensusChannels := map[ChannelID]bool{
		ConsensusStateChannel: true,
		ConsensusDataChannel:  true,
		ConsensusVoteChannel:  true,
		VoteSetBitsChannel:    true,
	}
	wrappers := (&consensus.Message{}).XXX_OneofWrappers()
	require.NotEmpty(t, wrappers)

	for _, wrapper := range wrappers {
		field := reflect.TypeOf(wrapper).Elem().Field(0)
		msg, ok := reflect.New(field.Type.Elem()).Interface().(proto.Message)
		require.True(t, ok, "%s does not hold a proto message", field.Name)

		t.Run(field.Name, func(t *testing.T) {
			var channel ChannelID
			require.NotPanics(t, func() { channel = ResolveChannelID(msg) })
			assert.True(t, consensusChannels[channel], "resolved to channel %#x", channel)
		})
	}
}

func TestResolveChannelIDSendsLivenessMessagesOnTheStateChannel(t *testing.T) {
	for _, msg := range []proto.Message{
		&consensus.ProposerHeartbeat{},
		&consensus.RoundSkipVote{},
		&consensus.RoundSkipCertificate{},
	} {
		assert.Equal(t, ConsensusStateChannel, ResolveChannelID(msg), "%T", msg)
	}
}
