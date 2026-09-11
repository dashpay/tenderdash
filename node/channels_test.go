package node

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/libs/log"
)

// TestNodeRegistersEveryChannelUpFront checks that every channel the node's
// reactors open is among the descriptors the transport is created with. A
// channel missing there is registered with the transport only when its
// reactor starts, after the router already accepts and dials peers, so a
// connection made in that window drops a peer that speaks on it with
// "unknown channel".
func TestNodeRegistersEveryChannelUpFront(t *testing.T) {
	cfg, err := config.ResetTestRoot(t.TempDir(), t.Name())
	require.NoError(t, err)
	defer os.RemoveAll(cfg.RootDir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ns, err := newDefaultNode(ctx, cfg, log.NewTestingLogger(t))
	require.NoError(t, err)
	n, ok := ns.(*nodeImpl)
	require.True(t, ok)
	t.Cleanup(func() {
		cancel()
		n.Wait()
	})
	t.Cleanup(leaktest.CheckTimeout(t, time.Second))

	require.NoError(t, n.Start(ctx))

	upFront := channelDescriptors(cfg)
	opened := n.NodeInfo().Channels.ToSlice()
	require.NotEmpty(t, opened)
	for _, id := range opened {
		assert.True(t, slices.ContainsFunc(upFront, func(desc *p2p.ChannelDescriptor) bool {
			return uint16(desc.ID) == id
		}), "channel %#x is opened by a reactor but not registered with the transport up front", id)
	}
}
