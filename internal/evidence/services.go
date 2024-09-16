package evidence

import (
	"github.com/dashpay/tenderdash/types"
)

//go:generate ../../scripts/mockery_generate.sh BlockStore

type BlockStore interface {
	LoadBlockMeta(height int64) *types.BlockMeta
	LoadBlockCommit(height int64) *types.Commit
	Base() int64
	Height() int64
}
