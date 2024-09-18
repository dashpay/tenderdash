package kvstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/config"
	validatorscoring "github.com/dashpay/tenderdash/internal/consensus/versioned/proposer"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func TestVerifyBlockCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, err := config.ResetTestRoot(t.TempDir(), "block_sync_reactor_test")
	require.NoError(t, err)
	defer os.RemoveAll(cfg.RootDir)

	logger := log.NewNopLogger()

	genDoc, privVals := factory.RandGenesisDoc(4, factory.ConsensusParams())
	height := genDoc.InitialHeight
	state, err := sm.MakeGenesisState(genDoc)
	require.NoError(t, err)

	kvstore := newKvApp(
		ctx, t,
		genDoc.InitialHeight,
		WithCommitVerification(),
		WithValidatorSetUpdates(map[int64]abci.ValidatorSetUpdate{
			1: types.TM2PB.ValidatorUpdates(state.Validators),
		}),
	)
	txs := []types.Tx{[]byte("key=val")}
	executor := blockExecutor{
		logger:   logger,
		state:    state,
		privVals: privVals,
	}
	block := executor.createBlock(txs, &types.Commit{})
	commit, err := executor.commit(ctx, block)
	require.NoError(t, err)
	reqPrep := abci.RequestPrepareProposal{
		Txs:        [][]byte{txs[0]},
		Height:     height,
		MaxTxBytes: 40960,
	}
	respPrep, err := kvstore.PrepareProposal(ctx, &reqPrep)
	require.NoError(t, err)
	assert.Len(t, respPrep.TxRecords, 1)
	require.Equal(t, 1, len(respPrep.TxResults))
	require.False(t, respPrep.TxResults[0].IsErr(), respPrep.TxResults[0].Log)

	pbBlock, err := block.ToProto()
	require.NoError(t, err)
	blockID := block.BlockID(nil)
	pbBlockID := blockID.ToProto()
	reqFb := &abci.RequestFinalizeBlock{
		Height:  height,
		Commit:  commit.ToCommitInfo(),
		Block:   pbBlock,
		BlockID: &pbBlockID,
	}
	_, err = kvstore.FinalizeBlock(ctx, reqFb)
	require.NoError(t, err)
}

type blockExecutor struct {
	logger   log.Logger
	state    sm.State
	privVals []types.PrivValidator
}

func (e *blockExecutor) createBlock(txs types.Txs, commit *types.Commit) *types.Block {
	if commit == nil {
		commit = &types.Commit{}
	}
	proposer := getProposerFromState(e.state, e.state.LastBlockHeight+1, 0)
	block := e.state.MakeBlock(
		e.state.LastBlockHeight+1,
		txs,
		commit,
		nil,
		proposer.ProTxHash,
		1,
	)
	return block
}

func (e *blockExecutor) commit(ctx context.Context, block *types.Block) (*types.Commit, error) {
	qt := e.state.Validators.QuorumType
	qh := e.state.Validators.QuorumHash

	vs := types.NewVoteSet(e.state.ChainID, block.Height, 0, tmproto.PrecommitType, e.state.Validators)
	for i, pv := range e.privVals {
		proTxHash, err := pv.GetProTxHash(ctx)
		if err != nil {
			return nil, err
		}
		vote := &types.Vote{
			Type:               tmproto.PrecommitType,
			Height:             block.Height,
			Round:              0,
			BlockID:            block.BlockID(nil),
			ValidatorProTxHash: proTxHash,
			ValidatorIndex:     int32(i),
			VoteExtensions:     nil,
		}
		pbVote := vote.ToProto()
		err = pv.SignVote(ctx, e.state.ChainID, qt, qh, pbVote, e.logger)
		if err != nil {
			return nil, err
		}
		vote.BlockSignature = pbVote.BlockSignature
		added, err := vs.AddVote(vote)
		if err != nil {
			return nil, err
		}
		if !added {
			return nil, errors.New("vote wasn't added to vote-set")
		}
	}
	return vs.MakeCommit(), nil
}

// GetProposerFromState returns the proposer for the given height and round.
//
// This function is a copy of the one in internal/state/test/factory/block.go
// to avoid a circular dependency.
func getProposerFromState(state sm.State, height int64, round int32) *types.Validator {
	vs, err := validatorscoring.NewProposerStrategy(
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
