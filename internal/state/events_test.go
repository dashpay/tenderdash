package state

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/types"
	"github.com/tendermint/tendermint/types/mocks"
)

func TestEmptyEventSet(t *testing.T) {
	publisher := &mocks.BlockEventPublisher{}
	es := EventSet{}
	err := es.Publish(publisher)
	require.NoError(t, err)
}

func TestEventSet(t *testing.T) {
	type mockEvidence struct{ types.Evidence }
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
