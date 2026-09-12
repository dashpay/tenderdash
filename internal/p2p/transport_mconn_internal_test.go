package p2p

import (
	"regexp"
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

// TestMConnTransportAddChannelDescriptorsWarnsAboutLateChannel checks that
// registering a channel the transport was not created with is reported. Opening
// such a channel succeeds, and only peers that speak on it over a connection
// established earlier are dropped, so without this warning a channel missing
// from the node's up-front list leaves no trace at all.
func TestMConnTransportAddChannelDescriptorsWarnsAboutLateChannel(t *testing.T) {
	logger := log.NewTestingLogger(t)
	// The logger fails the test during cleanup if nothing matched.
	logger.AssertMatch(regexp.MustCompile("channel registered after the transport was created"))

	transport := NewMConnTransport(logger, conn.DefaultMConnConfig(),
		[]*ChannelDescriptor{{ID: 0x00, Priority: 1, Name: "pex"}}, MConnTransportOptions{})

	transport.AddChannelDescriptors([]*ChannelDescriptor{{ID: 0x38, Priority: 6, Name: "evidence"}})
	require.Len(t, transport.channelDescs, 2)
}
