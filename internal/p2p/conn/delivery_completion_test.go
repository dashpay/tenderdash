package conn

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChannelCompletionRunsAfterFinalPacket(t *testing.T) {
	completed := false
	ch := &channel{
		desc:                    ChannelDescriptor{ID: 1},
		sendQueue:               make(chan outboundMessage, 1),
		maxPacketMsgPayloadSize: 2,
	}
	ch.sendQueue <- outboundMessage{bytes: []byte{1, 2, 3}, onSent: func() { completed = true }}
	require.True(t, ch.isSendPending())

	_, err := ch.writePacketMsgTo(&bytes.Buffer{})
	require.NoError(t, err)
	require.False(t, completed)

	_, err = ch.writePacketMsgTo(&bytes.Buffer{})
	require.NoError(t, err)
	require.True(t, completed)
}
