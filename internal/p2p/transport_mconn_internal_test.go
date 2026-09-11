package p2p

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dashpay/tenderdash/internal/p2p/conn"
	"github.com/dashpay/tenderdash/libs/log"
)

// TestMConnTransportAddChannelDescriptorsSkipsRegisteredChannels checks that a
// channel registered up front is not registered again when the router opens
// it, and that a descriptor snapshot taken for an earlier connection is left
// untouched by later registrations.
func TestMConnTransportAddChannelDescriptorsSkipsRegisteredChannels(t *testing.T) {
	pex := &ChannelDescriptor{ID: 0x00, Priority: 1}
	evidence := &ChannelDescriptor{ID: 0x38, Priority: 6}
	transport := NewMConnTransport(log.NewNopLogger(), conn.DefaultMConnConfig(),
		[]*ChannelDescriptor{pex}, MConnTransportOptions{})

	snapshot := transport.channelDescs
	transport.AddChannelDescriptors([]*ChannelDescriptor{{ID: 0x00, Priority: 1}, evidence})

	assert.Equal(t, []*ChannelDescriptor{pex, evidence}, transport.channelDescs)
	assert.Equal(t, []*ChannelDescriptor{pex}, snapshot)
}
