package consensus

import (
	"testing"

	"github.com/stretchr/testify/require"

	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func TestCommitExpectations(t *testing.T) {
	expected := tmproto.VoteExtensions{{Type: tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW, Extension: []byte("a")}}
	commit := func(height int64, round int32, block byte, exts tmproto.VoteExtensions) *types.Commit {
		return &types.Commit{Height: height, Round: round, BlockID: types.BlockID{Hash: []byte{block}},
			ThresholdVoteExtensions: exts}
	}

	t.Run("keyed by height, round and block", func(t *testing.T) {
		var e commitExpectations
		require.Nil(t, e.lookup(commit(5, 1, 1, nil)))
		learned := e.learn(commit(5, 1, 1, nil), expected)
		require.Same(t, learned, e.lookup(commit(5, 1, 1, expected)), "the vector is not part of the key")
		require.Nil(t, e.lookup(commit(5, 2, 1, nil)), "another round")
		require.Nil(t, e.lookup(commit(5, 1, 2, nil)), "another block")
		require.Nil(t, e.lookup(commit(6, 1, 1, nil)), "another height")
	})

	t.Run("another height discards the expectations", func(t *testing.T) {
		var e commitExpectations
		e.learn(commit(5, 1, 1, nil), expected)
		e.learn(commit(6, 1, 1, nil), expected)
		require.Nil(t, e.lookup(commit(5, 1, 1, nil)))
		require.Len(t, e.byRound, 1)
	})

	t.Run("matches only the expected vector, ignoring signatures", func(t *testing.T) {
		exp := commitExpectation{extensions: expected}
		signed := *expected[0]
		signed.Signature = []byte("signature")
		require.True(t, exp.matches(commit(5, 1, 1, tmproto.VoteExtensions{&signed})))
		require.False(t, exp.matches(commit(5, 1, 1, nil)))
		require.False(t, exp.matches(commit(5, 1, 1, append(expected, expected[0]))))
	})
}
