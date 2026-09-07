package consensus

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

// TestCommitIsCheckedAgainstEveryFieldOfTheBlockID covers the route into a
// permanent halt that involves no proposal at all. A commit names a block by
// hash, part set header and state ID. The first two are compared against the
// block this round holds; the third was not, so a commit agreeing on both while
// naming a different state ID was applied against this block and then recorded
// with its own BlockID. The next height compares that record against the state
// the block produced, they disagree, and no proposer at any round can satisfy
// both -- the disagreement is sealed by a threshold signature over a finalized
// height on one side and persisted state on the other.
//
// Each field is asked separately so an operator is told which one disagreed.
func TestCommitIsCheckedAgainstEveryFieldOfTheBlockID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = dash.ContextWithProTxHash(ctx, make(types.ProTxHash, 32))

	block := &types.Block{
		Header:     *factory.MakeHeader(t, &types.Header{Height: 10}),
		LastCommit: &types.Commit{},
	}
	parts, err := block.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)
	honest := block.BlockID(parts)
	require.NotEmpty(t, honest.StateID)

	otherParts, err := (&types.Block{
		Header:     *factory.MakeHeader(t, &types.Header{Height: 10, CoreChainLockedHeight: 9}),
		LastCommit: &types.Commit{},
	}).MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)

	testCases := []struct {
		name    string
		blockID func() types.BlockID
		wantErr string
	}{
		{
			name:    "disagrees on the state ID",
			blockID: func() types.BlockID { return forgeField(t, honest, "state_id") },
			wantErr: "state ID does not match",
		},
		{
			name:    "disagrees on the hash",
			blockID: func() types.BlockID { return forgeField(t, honest, "hash") },
			wantErr: "does not hash to commit hash",
		},
		{
			name: "disagrees on the part set header",
			blockID: func() types.BlockID {
				forged := honest
				forged.PartSetHeader = otherParts.Header()
				return forged
			},
			wantErr: "ProposalBlockParts header",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// blockExec is deliberately nil: every rejection under test must be
			// reached before the block is handed to the application, so a test that
			// starts passing by reaching it would panic rather than pass quietly.
			action := &TryAddCommitAction{logger: log.NewNopLogger()}
			stateData := StateData{
				logger: log.NewNopLogger(),
				RoundState: cstypes.RoundState{
					Height:             10,
					ProposalBlock:      block,
					ProposalBlockParts: parts,
				},
			}
			commit := &types.Commit{Height: 10, Round: 0, BlockID: tc.blockID()}

			verified, err := action.verifyCommitBlock(ctx, &stateData, commit)

			assert.False(t, verified, "a commit whose block ID misdescribes the block must not be applied")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr,
				"the operator must be told which field disagreed")
		})
	}
}
