package consensus

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/types"
)

func TestCommitCandidates(t *testing.T) {
	commit := func(round int32) *types.Commit { return &types.Commit{Height: 5, Round: round} }
	popAll := func(c *commitCandidates, height int64) []commitCandidate {
		var out []commitCandidate
		for {
			next, ok := c.pop(height)
			if !ok {
				return out
			}
			out = append(out, next)
		}
	}

	t.Run("a peer overwrites only its own slot, which keeps its place", func(t *testing.T) {
		var c commitCandidates
		first, second, replacement := commit(0), commit(1), commit(2)
		c.add(5, first, "a", false)
		c.add(5, second, "b", true)
		c.add(5, replacement, "a", false)
		require.Equal(t, []commitCandidate{
			{commit: replacement, peerID: "a"},
			{commit: second, peerID: "b", fromReplay: true},
		}, popAll(&c, 5))
	})

	t.Run("every peer has a slot", func(t *testing.T) {
		var c commitCandidates
		const peers = 100
		for i := range peers {
			c.add(5, commit(0), types.NodeID(fmt.Sprint(i)), false)
		}
		got := popAll(&c, 5)
		require.Len(t, got, peers)
		require.Equal(t, types.NodeID(fmt.Sprint(peers-1)), got[len(got)-1].peerID)
	})

	t.Run("a disconnected peer's slot is freed", func(t *testing.T) {
		queue := newMsgInfoQueue()
		c := commitCandidates{connected: queue.peerConnected}
		for _, peer := range []types.NodeID{"a", "b", "c"} {
			queue.admitPeer(peer)
		}
		c.add(5, commit(0), "a", false)
		c.add(5, commit(0), "b", false)
		c.add(5, commit(0), "replayed", true)
		c.add(5, commit(0), "", false)
		queue.purgePeer("a")
		c.add(5, commit(0), "c", false)
		peers := make([]types.NodeID, 0, 4)
		for _, got := range popAll(&c, 5) {
			peers = append(peers, got.peerID)
		}
		require.Equal(t, []types.NodeID{"b", "replayed", "", "c"}, peers,
			"replayed and local entries outlive any connection")

		queue.admitPeer("a")
		c.add(5, commit(1), "a", false)
		got := popAll(&c, 5)
		require.Len(t, got, 1, "a reconnected peer gets a slot again")
	})

	t.Run("another height discards the candidates", func(t *testing.T) {
		var c commitCandidates
		c.add(5, commit(0), "a", false)
		_, ok := c.pop(6)
		require.False(t, ok)
		c.add(5, commit(0), "a", false)
		c.add(6, commit(0), "b", false)
		require.Empty(t, popAll(&c, 5))
	})
}
