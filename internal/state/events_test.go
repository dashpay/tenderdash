package state

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/types"
	"github.com/tendermint/tendermint/types/mocks"
)

type mockEvidence struct{ types.Evidence }

func TestEmptyEventSet(t *testing.T) {
	publisher := &mocks.BlockEventPublisher{}
	es := EventSet{}
	err := es.Publish(publisher)
	require.NoError(t, err)
}

func TestEventSetError(t *testing.T) {
	block := &types.Block{
		Header:   types.Header{Height: 1000},
		Evidence: types.EvidenceList{mockEvidence{}},
		Data:     types.Data{Txs: types.Txs{types.Tx{}}},
	}
	blockID := types.BlockID{}
	ucs := CurrentRoundState{
		response:  abci.ResponseProcessProposal{},
		TxResults: []*abci.ExecTxResult{{}},
	}
	fbResp := abci.ResponseFinalizeBlock{}
	validatorSet := &types.ValidatorSet{}
	es := NewFullEventSet(block, blockID, ucs, &fbResp, validatorSet)
	publisher := &mocks.BlockEventPublisher{}
	publisher.
		On("PublishEventNewBlock", mock.Anything).
		Once().
		Return(errors.New("PublishEventNewBlock error"))
	publisher.
		On("PublishEventNewBlockHeader", mock.Anything).
		Once().
		Return(errors.New("PublishEventNewBlockHeader error"))
	publisher.
		On("PublishEventNewEvidence", mock.Anything).
		Once().
		Return(errors.New("PublishEventNewEvidence error"))
	publisher.
		On("PublishEventTx", mock.Anything).
		Once().
		Return(errors.New("PublishEventTx error"))
	publisher.
		On("PublishEventValidatorSetUpdates", mock.Anything).
		Once().
		Return(errors.New("PublishEventValidatorSetUpdates error"))
	err := es.Publish(publisher)
	require.Contains(t, err.Error(), "PublishEventNewBlock error")
	require.Contains(t, err.Error(), "PublishEventNewBlockHeader error")
	require.Contains(t, err.Error(), "PublishEventNewEvidence error")
	require.Contains(t, err.Error(), "PublishEventTx error")
	require.Contains(t, err.Error(), "PublishEventValidatorSetUpdates error")
	mock.AssertExpectationsForObjects(t, publisher)
}

func TestEventSet(t *testing.T) {
	block := &types.Block{
		Header:   types.Header{Height: 1000},
		Evidence: types.EvidenceList{mockEvidence{}, mockEvidence{}},
		Data: types.Data{
			Txs: types.Txs{types.Tx{}, types.Tx{}},
		},
	}
	blockID := types.BlockID{}
	ppResp := abci.ResponseProcessProposal{}
	fbResp := abci.ResponseFinalizeBlock{}
	validatorSet := &types.ValidatorSet{}
	txResults := []*abci.ExecTxResult{{}, {}}
	publisher := &mocks.BlockEventPublisher{}
	publisher.
		On("PublishEventNewBlock", types.EventDataNewBlock{
			Block:               block,
			BlockID:             blockID,
			ResultFinalizeBlock: fbResp,
		}).
		Once().
		Return(nil)
	publisher.
		On("PublishEventNewBlockHeader", types.EventDataNewBlockHeader{
			Header:                block.Header,
			NumTxs:                2,
			ResultProcessProposal: ppResp,
			ResultFinalizeBlock:   fbResp,
		}).
		Once().
		Return(nil)
	publisher.
		On("PublishEventNewEvidence", types.EventDataNewEvidence{
			Evidence: block.Evidence[0],
			Height:   1000,
		}).
		Once().
		Return(nil)
	publisher.
		On("PublishEventNewEvidence", types.EventDataNewEvidence{
			Evidence: block.Evidence[1],
			Height:   1000,
		}).
		Once().
		Return(nil)
	publisher.
		On("PublishEventTx", types.EventDataTx{
			TxResult: abci.TxResult{
				Height: block.Height,
				Index:  0,
				Tx:     block.Txs[0],
				Result: *txResults[0],
			},
		}).
		Once().
		Return(nil)
	publisher.
		On("PublishEventTx", types.EventDataTx{
			TxResult: abci.TxResult{
				Height: block.Height,
				Index:  1,
				Tx:     block.Txs[1],
				Result: *txResults[1],
			},
		}).
		Once().
		Return(nil)
	publisher.
		On("PublishEventValidatorSetUpdates", types.EventDataValidatorSetUpdate{
			ValidatorSetUpdates: validatorSet.Validators,
			ThresholdPublicKey:  validatorSet.ThresholdPublicKey,
			QuorumHash:          validatorSet.QuorumHash,
		}).
		Once().
		Return(nil)
	es := EventSet{}
	es.
		WithNewBlock(block, blockID, fbResp).
		WthNewBlockHeader(block, ppResp, fbResp).
		WithValidatorSetUpdate(validatorSet).
		WithNewEvidences(block).
		WithTxs(block, txResults)
	err := es.Publish(publisher)
	require.NoError(t, err)
	mock.AssertExpectationsForObjects(t, publisher)
}
