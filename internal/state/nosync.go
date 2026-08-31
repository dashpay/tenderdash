package state

import (
	"os"

	dbm "github.com/cometbft/cometbft-db"
)

// unsafeNoSync drops the fsync from every state-store write. See the identical
// switch in internal/store: it is a benchmarking aid, not a production setting.
var unsafeNoSync = os.Getenv("TD_UNSAFE_NOSYNC") == "1"

func writeBatch(batch dbm.Batch) error {
	if unsafeNoSync {
		return batch.Write()
	}
	return batch.WriteSync()
}

func dbSet(db dbm.DB, key, value []byte) error {
	if unsafeNoSync {
		return db.Set(key, value)
	}
	return db.SetSync(key, value)
}
