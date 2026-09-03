package store

import (
	"os"

	dbm "github.com/cometbft/cometbft-db"
)

// unsafeNoSync drops the fsync from every block-store write. It exists for
// benchmarking: on macOS Go's File.Sync issues F_FULLFSYNC, which costs ~7ms
// per call and swamps everything else in a block-sync profile, while on Linux
// the same call is a plain fsync costing a fraction of that. Set
// TD_UNSAFE_NOSYNC=1 to take durability out of the measurement. Never set it on
// a node whose data you care about: a power loss can then leave the block store
// behind the application.
var unsafeNoSync = os.Getenv("TD_UNSAFE_NOSYNC") == "1"

func writeBatch(batch dbm.Batch) error {
	if unsafeNoSync {
		return batch.Write()
	}
	return batch.WriteSync()
}
