package db

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGoLevelDBReopen(t *testing.T) {
	dir := t.TempDir()
	db, err := NewGoLevelDB("state", dir)
	require.NoError(t, err)
	require.NoError(t, db.SetSync([]byte("deleted"), []byte("old")))

	batch := db.NewBatch()
	require.NoError(t, batch.Set([]byte("prefix/a"), []byte("value")))
	require.NoError(t, batch.Set([]byte("prefix/b"), []byte{}))
	require.NoError(t, batch.Delete([]byte("deleted")))
	require.NoError(t, batch.WriteSync())
	require.NoError(t, batch.Close())
	require.NoError(t, db.Close())

	db, err = NewGoLevelDB("state", dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	value, err := db.Get([]byte("deleted"))
	require.NoError(t, err)
	require.Nil(t, value)

	prefix := NewPrefixDB(db, []byte("prefix/"))
	iter, err := prefix.ReverseIterator(nil, nil)
	require.NoError(t, err)
	defer iter.Close()
	require.True(t, iter.Valid())
	require.Equal(t, []byte("b"), iter.Key())
	require.Empty(t, iter.Value())
	iter.Next()
	require.True(t, iter.Valid())
	require.Equal(t, []byte("a"), iter.Key())
	require.Equal(t, []byte("value"), iter.Value())
	iter.Next()
	require.False(t, iter.Valid())
	require.NoError(t, iter.Error())
}

func TestMemDBIteratorCloseReleasesWriter(t *testing.T) {
	db := NewMemDB()
	// Exceed the iterator's buffer so the producer must stop on early Close.
	for i := 0; i < 256; i++ {
		require.NoError(t, db.Set([]byte(fmt.Sprintf("%03d", i)), []byte("value")))
	}
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprintf("reverse=%t", reverse), func(t *testing.T) {
			var iter Iterator
			var err error
			if reverse {
				iter, err = db.ReverseIterator(nil, nil)
			} else {
				iter, err = db.Iterator(nil, nil)
			}
			require.NoError(t, err)
			require.True(t, iter.Valid())
			done := make(chan error, 1)
			go func() { done <- db.Set([]byte("writer"), []byte("value")) }()
			require.NoError(t, iter.Close())
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("writer blocked after closing iterator")
			}
		})
	}
}
