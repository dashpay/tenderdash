package consensus

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func TestCommitCandidates(t *testing.T) {
	commit := func(round int32) *types.Commit { return &types.Commit{Height: 5, Round: round} }
	popAll := func(c *commitCandidates, height int64) []commitCandidate { return c.take(height) }

	t.Run("a peer overwrites only its own slot, which keeps its place", func(t *testing.T) {
		var c commitCandidates
		first, second, replacement := commit(0), commit(1), commit(2)
		c.add(5, first, "a", false)
		c.add(5, second, "b", true)
		c.add(5, replacement, "a", false)
		require.Equal(t, []commitCandidate{
			{commit: replacement, key: newCommitKey(replacement), peerID: "a"},
			{commit: second, key: newCommitKey(second), peerID: "b", fromReplay: true},
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

	t.Run("holds finds a commit of the height in the peer's own slot", func(t *testing.T) {
		var c commitCandidates
		c.add(5, commit(0), "a", false)
		require.True(t, c.holds(5, "a", newCommitKey(commit(0))), "an equal commit from its sender is held")
		require.False(t, c.holds(5, "b", newCommitKey(commit(0))), "another peer's slot does not count")
		require.False(t, c.holds(5, "a", newCommitKey(commit(1))), "another round is another commit")
		require.False(t, c.holds(6, "a", newCommitKey(commit(0))), "another height holds nothing")
	})

	t.Run("another height discards the candidates", func(t *testing.T) {
		var c commitCandidates
		c.add(5, commit(0), "a", false)
		require.Empty(t, c.take(6))
		c.add(5, commit(0), "a", false)
		c.add(6, commit(0), "b", false)
		require.Empty(t, popAll(&c, 5))
	})
}

func TestCommitKey(t *testing.T) {
	base := &types.Commit{Height: 5, Round: 1, BlockID: types.BlockID{Hash: []byte{1}},
		ThresholdVoteExtensions: tmproto.VoteExtensions{{Extension: []byte("a")}}}
	require.Equal(t, newCommitKey(base), newCommitKey(base))

	sigOnly := *base
	sigOnly.ThresholdBlockSignature = []byte("other signature")
	require.Equal(t, newCommitKey(base), newCommitKey(&sigOnly), "the block signature is not part of the key")

	for name, change := range map[string]func(c *types.Commit){
		"height":    func(c *types.Commit) { c.Height++ },
		"round":     func(c *types.Commit) { c.Round++ },
		"block":     func(c *types.Commit) { c.BlockID = types.BlockID{Hash: []byte{2}} },
		"extension": func(c *types.Commit) { c.ThresholdVoteExtensions = tmproto.VoteExtensions{{Extension: []byte("b")}} },
		"split": func(c *types.Commit) {
			c.ThresholdVoteExtensions = tmproto.VoteExtensions{{Extension: []byte("a")}, {}}
		},
		"stripped": func(c *types.Commit) { c.ThresholdVoteExtensions = nil },
	} {
		changed := *base
		change(&changed)
		require.NotEqual(t, newCommitKey(base), newCommitKey(&changed), name)
	}
}
