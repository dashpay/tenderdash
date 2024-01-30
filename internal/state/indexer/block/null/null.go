package null

import (
	"context"
	"errors"

	"github.com/dashpay/tenderdash/internal/pubsub/query"
	"github.com/dashpay/tenderdash/internal/state/indexer"
	"github.com/dashpay/tenderdash/types"
)

var _ indexer.BlockIndexer = (*BlockerIndexer)(nil)

// TxIndex implements a no-op block indexer.
type BlockerIndexer struct{}

func (idx *BlockerIndexer) Has(_height int64) (bool, error) {
	return false, errors.New(`indexing is disabled (set 'tx_index = "kv"' in config)`)
}

func (idx *BlockerIndexer) Index(types.EventDataNewBlockHeader) error {
	return nil
}

func (idx *BlockerIndexer) Search(_ctx context.Context, _q *query.Query) ([]int64, error) {
	return []int64{}, nil
}
