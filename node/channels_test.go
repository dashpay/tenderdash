package node

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/p2p"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/types"
	"github.com/dashpay/tenderdash/version"
)

// TestChannelDescriptorsMatchAdvertisedChannels checks, for every node mode,
// that the descriptors the transport is created with are exactly the channels
// the node advertises in its NodeInfo, each registered once.
//
// A channel missing from the descriptors is registered with the transport only
// when its reactor starts, after the router already accepts and dials peers, so
// a connection made in that window drops a peer that speaks on it with
// "unknown channel". A channel registered twice gives every connection two
// conn.MConnection channels for one ID, of which the channel index keeps an
// arbitrary one and the other silently never receives.
func TestChannelDescriptorsMatchAdvertisedChannels(t *testing.T) {
	nodeKey := types.GenNodeKey()
	// Only ChainID is read out of the genesis doc by either NodeInfo builder.
	genDoc := &types.GenesisDoc{ChainID: "channel-descriptors-test"}

	for _, mode := range []string{config.ModeValidator, config.ModeFull, config.ModeSeed} {
		t.Run(mode, func(t *testing.T) {
			// Not t.Name(): it carries the subtest separator, which
			// ResetTestRoot feeds to os.MkdirTemp as a pattern.
			cfg, err := config.ResetTestRoot(t.TempDir(), "channel_descriptors_"+mode)
			require.NoError(t, err)
			cfg.Mode = mode

			registered := make(map[uint16]string)
			for _, desc := range channelDescriptors(cfg) {
				name, dup := registered[uint16(desc.ID)]
				require.False(t, dup,
					"channel %#x is registered twice, as %q and %q", desc.ID, name, desc.Name)
				registered[uint16(desc.ID)] = desc.Name
			}

			var nodeInfo types.NodeInfo
			if mode == config.ModeSeed {
				nodeInfo, err = makeSeedNodeInfo(cfg, nodeKey, genDoc, sm.State{})
			} else {
				nodeInfo, err = makeNodeInfo(cfg, nodeKey, nil, nil, genDoc, version.Consensus{})
			}
			// Validate, called by both builders, rejects a duplicate or
			// over-long advertised channel list.
			require.NoError(t, err)

			advertised := nodeInfo.Channels.ToSlice()
			require.NotEmpty(t, advertised)
			for _, id := range advertised {
				assert.Contains(t, registered, id,
					"channel %#x is advertised in NodeInfo but not registered with the transport up front", id)
			}

			// p2p.ErrorChannel is the one exception in both directions: the p2p
			// client opens it lazily on first use rather than through a
			// reactor, and it is deliberately not advertised to peers.
			want := advertised
			if mode != config.ModeSeed {
				want = append(want, uint16(p2p.ErrorChannel))
			}
			got := make([]uint16, 0, len(registered))
			for id := range registered {
				got = append(got, id)
			}
			assert.ElementsMatch(t, want, got,
				"the transport is created with channels no reactor of a %s node opens", mode)
		})
	}
}
