// Package dbspy provides a dbm.DB wrapper that counts how each write was
// committed, so a test can tell a durable write from one that skipped the
// fsync.
package dbspy

import dbm "github.com/cometbft/cometbft-db"

// DB counts the syncing and non-syncing writes made through it.
type DB struct {
	dbm.DB
	Sets, SyncSets, Writes, SyncWrites int
}

// New wraps db.
func New(db dbm.DB) *DB { return &DB{DB: db} }

func (db *DB) Set(key, value []byte) error {
	db.Sets++
	return db.DB.Set(key, value)
}

func (db *DB) SetSync(key, value []byte) error {
	db.SyncSets++
	return db.DB.SetSync(key, value)
}

func (db *DB) NewBatch() dbm.Batch { return &batch{Batch: db.DB.NewBatch(), db: db} }

type batch struct {
	dbm.Batch
	db *DB
}

func (b *batch) Write() error {
	b.db.Writes++
	return b.Batch.Write()
}

func (b *batch) WriteSync() error {
	b.db.SyncWrites++
	return b.Batch.WriteSync()
}
