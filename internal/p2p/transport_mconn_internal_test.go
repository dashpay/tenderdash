package p2p

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/p2p/conn"
	"github.com/dashpay/tenderdash/libs/log"
)

// TestMConnTransportAddChannelDescriptorsSkipsRegisteredChannels checks that a
// channel registered up front is not registered a second time when the router
// opens it, and that a descriptor snapshot an earlier connection took is left
// untouched by later registrations.
func TestMConnTransportAddChannelDescriptorsSkipsRegisteredChannels(t *testing.T) {
	pex := &ChannelDescriptor{ID: 0x00, Priority: 1, Name: "pex"}
	// Same ID, distinct pointer: this is what a reactor opening an already
	// registered channel hands to the transport.
	pexAgain := &ChannelDescriptor{ID: 0x00, Priority: 1, Name: "pex"}
	evidence := &ChannelDescriptor{ID: 0x38, Priority: 6, Name: "evidence"}

	transport := NewMConnTransport(log.NewNopLogger(), conn.DefaultMConnConfig(),
		// Spare capacity, so an in-place append would write into the array the
		// snapshot below shares - without it the copy-on-write assertion holds
		// for any implementation.
		append(make([]*ChannelDescriptor, 0, 4), pex),
		MConnTransportOptions{})

	snapshot := transport.channelDescs
	require.Less(t, len(snapshot), cap(snapshot), "the snapshot must have room to grow in place")

	transport.AddChannelDescriptors([]*ChannelDescriptor{pexAgain, evidence})

	require.Len(t, transport.channelDescs, 2, "channel 0x00 was registered twice")
	// The first registration wins: replacing it would make the channel index of
	// every later connection depend on registration order. Identity, not
	// equality - pexAgain is indistinguishable from pex by value.
	assert.Same(t, pex, transport.channelDescs[0])
	assert.Same(t, evidence, transport.channelDescs[1])

	require.Len(t, snapshot, 1, "a snapshot taken before the registration was appended to")
	assert.Same(t, pex, snapshot[0])
}
