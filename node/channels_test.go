package node

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/evidence"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/pex"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/types"
	"github.com/dashpay/tenderdash/version"
)

// openedChannels returns, per node mode, the descriptors every component of a
// node passes to Router.OpenChannel. Each entry is the same expression its
// caller uses - p2p client (node.go), consensus, state sync, evidence and PEX
// reactors - so that adding a channel to any of those sets, or dropping one from
// the transport's up-front list, breaks this test rather than only connections
// made during startup.
func openedChannels(cfg *config.Config) []*p2p.ChannelDescriptor {
	opened := []*p2p.ChannelDescriptor{pex.ChannelDescriptor()}
	if cfg.Mode == config.ModeSeed {
		return opened
	}
	opened = append(opened, evidence.GetChannelDescriptor())
	for _, set := range []map[p2p.ChannelID]*p2p.ChannelDescriptor{
		p2p.ChannelDescriptors(cfg),
		p2p.ConsensusChannelDescriptors(),
		p2p.StatesyncChannelDescriptors(),
	} {
		for _, desc := range set {
			opened = append(opened, desc)
		}
	}
	return opened
}

// TestChannelDescriptorsCoverEveryOpenedChannel checks, for every node mode,
// that the transport is created with each channel the node later opens, exactly
// once, and that NodeInfo advertises the same set.
//
// A channel missing from the descriptors is registered with the transport only
// when its owner opens it, after the router already accepts and dials peers, so
// a connection made in that window drops a peer that speaks on it with "unknown
// channel" - and nothing else fails, because Router.OpenChannel does not
// consult the up-front list. A channel registered twice gives every connection
// two conn.MConnection channels for one ID, of which the channel index keeps an
// arbitrary one while the other silently never receives.
func TestChannelDescriptorsCoverEveryOpenedChannel(t *testing.T) {
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

			registered := make(map[p2p.ChannelID]string)
			for _, desc := range channelDescriptors(cfg) {
				name, dup := registered[desc.ID]
				require.False(t, dup,
					"channel %#x is registered twice, as %q and %q", desc.ID, name, desc.Name)
				registered[desc.ID] = desc.Name
			}

			for _, desc := range openedChannels(cfg) {
				assert.Contains(t, registered, desc.ID,
					"channel %#x (%q) is opened by a %s node but not registered with the transport up front",
					desc.ID, desc.Name, mode)
			}

			var nodeInfo types.NodeInfo
			if mode == config.ModeSeed {
				nodeInfo, err = makeSeedNodeInfo(cfg, nodeKey, genDoc, sm.State{})
			} else {
				nodeInfo, err = makeNodeInfo(cfg, nodeKey, nil, nil, genDoc, version.Consensus{})
			}
			// Validate, called by both builders, rejects a duplicate or
			// over-long (types.maxNumChannels) advertised channel list.
			require.NoError(t, err)

			advertised := nodeInfo.Channels.ToSlice()
			assert.NotContains(t, advertised, uint16(p2p.ErrorChannel),
				"the error channel carries no peer traffic and must not be advertised")
			want := make([]uint16, 0, len(registered))
			for id := range registered {
				if id != p2p.ErrorChannel {
					want = append(want, uint16(id))
				}
			}
			assert.ElementsMatch(t, want, advertised,
				"a %s node advertises channels that differ from the ones it registers", mode)
		})
	}
}
