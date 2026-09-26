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

	t.Run("the number of slots is capped", func(t *testing.T) {
		var c commitCandidates
		for i := range maxCommitCandidates + 1 {
			c.add(5, commit(0), types.NodeID(fmt.Sprint(i)), false)
		}
		got := popAll(&c, 5)
		require.Len(t, got, maxCommitCandidates)
		require.Equal(t, types.NodeID(fmt.Sprint(maxCommitCandidates-1)), got[len(got)-1].peerID,
			"commits past the cap are dropped, the earlier ones kept")
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
