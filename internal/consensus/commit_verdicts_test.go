package consensus

import (
	"testing"

	"github.com/stretchr/testify/require"

	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

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

func TestCommitVerdicts(t *testing.T) {
	key := func(height int64, round int32) commitKey { return commitKey{height: height, round: round} }

	t.Run("records verdicts of one height", func(t *testing.T) {
		var v commitVerdicts
		_, known := v.lookup(key(5, 0))
		require.False(t, known)
		v.record(key(5, 0), false)
		v.record(key(5, 1), true)
		accepted, known := v.lookup(key(5, 0))
		require.True(t, known)
		require.False(t, accepted)
		accepted, known = v.lookup(key(5, 1))
		require.True(t, known)
		require.True(t, accepted)
	})

	t.Run("another height discards the verdicts", func(t *testing.T) {
		var v commitVerdicts
		v.record(key(5, 0), false)
		_, known := v.lookup(key(6, 0))
		require.False(t, known)
		v.record(key(6, 0), true)
		_, known = v.lookup(key(5, 0))
		require.False(t, known)
		require.Len(t, v.accepted, 1)
	})

	t.Run("is bounded and drops the oldest verdict", func(t *testing.T) {
		var v commitVerdicts
		for round := range int32(maxCommitVerdicts + 1) {
			v.record(key(5, round), false)
		}
		require.Len(t, v.accepted, maxCommitVerdicts)
		require.Len(t, v.order, maxCommitVerdicts)
		_, known := v.lookup(key(5, 0))
		require.False(t, known, "the oldest verdict makes room")
		_, known = v.lookup(key(5, maxCommitVerdicts))
		require.True(t, known)
		v.record(key(5, maxCommitVerdicts), true)
		require.Len(t, v.order, maxCommitVerdicts, "a new verdict for a known key takes no room")
	})
}
