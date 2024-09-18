package factory

import (
	"context"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/abci/example/kvstore"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	selectproposer "github.com/dashpay/tenderdash/internal/consensus/versioned/selectproposer"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/types"
)

func MakeBlocks(ctx context.Context, t *testing.T, n int, state *sm.State, privVals []types.PrivValidator, proposedAppVersion uint64) []*types.Block {
	t.Helper()

	blocks := make([]*types.Block, 0, n)

	var (
		prevBlock     *types.Block
		prevBlockMeta *types.BlockMeta
	)

	appHeight := byte(0x01)
	for i := 0; i < n; i++ {
		height := int64(i + 1)

		block, parts := makeBlockAndPartSet(ctx, t, *state, prevBlock, prevBlockMeta, privVals, height, proposedAppVersion)
		blocks = append(blocks, block)

		prevBlock = block
		prevBlockMeta = types.NewBlockMeta(block, parts, 0)

		// update state
		appHash := make([]byte, crypto.DefaultAppHashSize)
		binary.BigEndian.PutUint64(appHash, uint64(height))
		changes, err := state.NewStateChangeset(ctx, sm.RoundParams{AppHash: appHash})
		require.NoError(t, err)
		err = changes.UpdateState(state)
		assert.NoError(t, err)
		appHeight++
		state.LastBlockHeight = height
	}

	return blocks
}

func MakeBlock(state sm.State, height int64, c *types.Commit, proposedAppVersion uint64) (*types.Block, error) {
	if state.LastBlockHeight != (height - 1) {
		return nil, fmt.Errorf("requested height %d should be 1 more than last block height %d", height, state.LastBlockHeight)
	}
	proposer := GetProposerFromState(state, height, 0)
	block := state.MakeBlock(
		height,
		factory.MakeNTxs(state.LastBlockHeight, 10),
		c,
		nil,
		proposer.ProTxHash,
		proposedAppVersion,
	)
	var err error
	block.AppHash = make([]byte, crypto.DefaultAppHashSize)
	if block.ResultsHash, err = abci.TxResultsHash(factory.ExecTxResults(block.Txs)); err != nil {
		return nil, err
	}
	// this should be set by PrepareProposal, but we don't always call PrepareProposal
	if block.Version.App == 0 {
		block.Version.App = kvstore.ProtocolVersion
	}

	return block, nil
}

func makeBlockAndPartSet(
	ctx context.Context,
	t *testing.T,
	state sm.State,
	lastBlock *types.Block,
	lastBlockMeta *types.BlockMeta,
	privVals []types.PrivValidator,
	height int64,
	proposedAppVersion uint64,
) (*types.Block, *types.PartSet) {
	t.Helper()

	quorumSigns := &types.CommitSigns{QuorumHash: state.LastValidators.QuorumHash}
	var ve types.VoteExtensions
	if lastBlock != nil && lastBlock.LastCommit != nil {
		ve = types.VoteExtensionsFromProto(lastBlock.LastCommit.ThresholdVoteExtensions...)
	}
	lastCommit := types.NewCommit(height-1, 0, types.BlockID{}, ve, quorumSigns)
	if height > 1 {
		var err error
		votes := make([]*types.Vote, len(privVals))
		for i, privVal := range privVals {
			votes[i], err = factory.MakeVote(
				ctx,
				privVal,
				state.Validators,
				lastBlock.Header.ChainID,
				1, lastBlock.Header.Height, 0, 2,
				lastBlockMeta.BlockID,
			)
			require.NoError(t, err)
		}

		thresholdSigns, err := types.NewSignsRecoverer(votes).Recover()
		require.NoError(t, err)
		lastCommit = types.NewCommit(
			lastBlock.Header.Height,
			0,
			lastBlockMeta.BlockID,
			types.VoteExtensionsFromProto(lastBlock.LastCommit.ThresholdVoteExtensions...),
			&types.CommitSigns{
				QuorumSigns: *thresholdSigns,
				QuorumHash:  state.LastValidators.QuorumHash,
			},
		)
	}
	proposer := GetProposerFromState(state, height, 0)
	block := state.MakeBlock(height, []types.Tx{}, lastCommit, nil, proposer.ProTxHash, proposedAppVersion)
	partSet, err := block.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)

	return block, partSet
}

func GetProposerFromState(state sm.State, height int64, round int32) *types.Validator {
	vs, err := selectproposer.NewProposerSelector(
		state.ConsensusParams,
		state.Validators.Copy(),
		state.LastBlockHeight,
		state.LastBlockRound,
		nil,
	)
	if err != nil {
		panic(fmt.Errorf("failed to create validator scoring strategy: %w", err))
	}
	proposer := vs.MustGetProposer(height, round)
	return proposer
}
