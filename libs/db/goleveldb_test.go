package db

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syndtr/goleveldb/leveldb/opt"
)

func TestGoLevelDBNewGoLevelDB(t *testing.T) {
	name := fmt.Sprintf("test_%x", randStr(12))
	dir := t.TempDir()

	// Test we can't open the db twice for writing
	wr1, err := NewGoLevelDB(name, dir)
	require.Nil(t, err)
	_, err = NewGoLevelDB(name, dir)
	require.NotNil(t, err)
	err = wr1.Close() // Close the db to release the lock
	require.Nil(t, err)

	// Test we can open the db twice for reading only
	ro1, err := NewGoLevelDBWithOpts(name, dir, &opt.Options{ReadOnly: true})
	require.Nil(t, err)
	defer ro1.Close()
	ro2, err := NewGoLevelDBWithOpts(name, dir, &opt.Options{ReadOnly: true})
	require.Nil(t, err)
	defer ro2.Close()
}

func BenchmarkGoLevelDBRandomReadsWrites(b *testing.B) {
	name := fmt.Sprintf("test_%x", randStr(12))
	dir := b.TempDir()
	db, err := NewGoLevelDB(name, dir)
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		err = db.Close()
		require.NoError(b, err)
	}()

	benchmarkRandomReadsWrites(b, db)
}

func TestGoLevelDBBackend(t *testing.T) {
	name := fmt.Sprintf("test_%x", randStr(12))
	db, err := NewDB(name, GoLevelDBBackend, t.TempDir())
	require.NoError(t, err)
	defer db.Close()

	_, ok := db.(*GoLevelDB)
	assert.True(t, ok)
}
