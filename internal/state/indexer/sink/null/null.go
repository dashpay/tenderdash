package null

import (
	"context"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/internal/pubsub/query"
	"github.com/dashpay/tenderdash/internal/state/indexer"
	"github.com/dashpay/tenderdash/types"
)

var _ indexer.EventSink = (*EventSink)(nil)

// EventSink implements a no-op indexer.
type EventSink struct{}

func NewEventSink() indexer.EventSink {
	return &EventSink{}
}

func (nes *EventSink) Type() indexer.EventSinkType {
	return indexer.NULL
}

func (nes *EventSink) IndexBlockEvents(_bh types.EventDataNewBlockHeader) error {
	return nil
}

func (nes *EventSink) IndexTxEvents(_results []*abci.TxResult) error {
	return nil
}

func (nes *EventSink) SearchBlockEvents(_ctx context.Context, _q *query.Query) ([]int64, error) {
	return nil, nil
}

func (nes *EventSink) SearchTxEvents(_ctx context.Context, _q *query.Query) ([]*abci.TxResult, error) {
	return nil, nil
}

func (nes *EventSink) GetTxByHash(_hash []byte) (*abci.TxResult, error) {
	return nil, nil
}

func (nes *EventSink) HasBlock(_h int64) (bool, error) {
	return false, nil
}

func (nes *EventSink) Stop() error {
	return nil
}
