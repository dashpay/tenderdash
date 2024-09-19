package state

import (
	"fmt"

	"github.com/hashicorp/go-multierror"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/libs/math"
	"github.com/dashpay/tenderdash/types"
)

// EventSet is a set of events that are published immediately after the block is committed.
type EventSet struct {
	NewBlock           *types.EventDataNewBlock
	NewBlockHeader     *types.EventDataNewBlockHeader
	NewEvidence        []types.EventDataNewEvidence
	Txs                []types.EventDataTx
	ValidatorSetUpdate *types.EventDataValidatorSetUpdate
}

// NewFullEventSet returns a full EventSet
func NewFullEventSet(
	block *types.Block,
	blockID types.BlockID,
	uncommittedState CurrentRoundState,
	validatorsSet *types.ValidatorSet,
) EventSet {
	responseProcessProposal := uncommittedState.Params.ToProcessProposal()
	es := EventSet{}
	es.
		WithNewBlock(block, blockID, *responseProcessProposal).
		WthNewBlockHeader(block, *responseProcessProposal).
		WithNewEvidences(block).
		WithTxs(block, uncommittedState.TxResults).
		WithValidatorSetUpdate(validatorsSet)
	return es
}

// WithNewBlock adds types.EventDataNewBlock event to a set
func (e *EventSet) WithNewBlock(
	block *types.Block,
	blockID types.BlockID,
	responseProcessProposal abci.ResponseProcessProposal,
) *EventSet {
	e.NewBlock = &types.EventDataNewBlock{
		Block:                 block,
		BlockID:               blockID,
		ResultProcessProposal: responseProcessProposal,
	}
	return e
}

// WthNewBlockHeader adds types.EventDataNewBlockHeader event to a set
func (e *EventSet) WthNewBlockHeader(
	block *types.Block,
	ppResp abci.ResponseProcessProposal,
) *EventSet {
	e.NewBlockHeader = &types.EventDataNewBlockHeader{
		Header:                block.Header,
		NumTxs:                int64(len(block.Txs)),
		ResultProcessProposal: ppResp,
	}
	return e
}

// WithNewEvidences creates and adds types.EventDataNewEvidence events to a set
func (e *EventSet) WithNewEvidences(block *types.Block) *EventSet {
	e.NewEvidence = make([]types.EventDataNewEvidence, len(block.Evidence))
	for i, ev := range block.Evidence {
		e.NewEvidence[i] = types.EventDataNewEvidence{
			Evidence: ev,
			Height:   block.Height,
		}
	}
	return e
}

// WithTxs creates and adds types.EventDataTx events to a set
func (e *EventSet) WithTxs(block *types.Block, txResults []*abci.ExecTxResult) *EventSet {
	// sanity check
	if len(txResults) != len(block.Data.Txs) {
		panic(fmt.Sprintf("number of TXs (%d) and ABCI TX responses (%d) do not match",
			len(block.Data.Txs), len(txResults)))
	}
	e.Txs = make([]types.EventDataTx, len(txResults))
	for i, tx := range block.Data.Txs {
		e.Txs[i] = types.EventDataTx{
			TxResult: abci.TxResult{
				Height: block.Height,
				Index:  math.MustConvertUint32(i),
				Tx:     tx,
				Result: *(txResults[i]),
			},
		}
	}
	return e
}

// WithValidatorSetUpdate creates and adds types.EventDataValidatorSetUpdate event to a set
func (e *EventSet) WithValidatorSetUpdate(validatorSet *types.ValidatorSet) *EventSet {
	e.ValidatorSetUpdate = &types.EventDataValidatorSetUpdate{
		ValidatorSetUpdates: validatorSet.Validators,
		ThresholdPublicKey:  validatorSet.ThresholdPublicKey,
		QuorumHash:          validatorSet.QuorumHash,
	}
	return e
}

// Publish publishes events that were added to a EventSet
// if Tenderdash crashes before commit, some or all of these events may be published again
func (e *EventSet) Publish(publisher types.BlockEventPublisher) error {
	var errs error
	if e.NewBlock != nil {
		err := publisher.PublishEventNewBlock(*e.NewBlock)
		if err != nil {
			errs = multierror.Append(errs, err)
		}
	}
	if e.NewBlockHeader != nil {
		err := publisher.PublishEventNewBlockHeader(*e.NewBlockHeader)
		if err != nil {
			errs = multierror.Append(errs, err)
		}
	}
	for _, evidence := range e.NewEvidence {
		err := publisher.PublishEventNewEvidence(evidence)
		if err != nil {
			errs = multierror.Append(errs, err)
		}
	}
	for _, tx := range e.Txs {
		err := publisher.PublishEventTx(tx)
		if err != nil {
			errs = multierror.Append(errs, err)
		}
	}
	if e.ValidatorSetUpdate != nil {
		err := publisher.PublishEventValidatorSetUpdates(*e.ValidatorSetUpdate)
		if err != nil {
			errs = multierror.Append(errs, err)
		}
	}
	return errs
}
