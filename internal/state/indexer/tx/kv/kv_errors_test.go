package kv

import (
	"context"
	"errors"
	"fmt"
	"testing"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/require"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/internal/pubsub/query"
	"github.com/dashpay/tenderdash/types"
)

var errInjected = errors.New("injected database error")

// errorDB wraps a dbm.DB and lets a test inject failures into Get and into the
// iterators it hands out, including failures that only surface after iteration
// via Iterator.Error().
type errorDB struct {
	dbm.DB

	failGet           bool
	failIteratorOpen  bool
	failIteratorError bool
}

func (d *errorDB) Get(key []byte) ([]byte, error) {
	if d.failGet {
		return nil, errInjected
	}
	return d.DB.Get(key)
}

func (d *errorDB) Iterator(start, end []byte) (dbm.Iterator, error) {
	if d.failIteratorOpen {
		return nil, errInjected
	}
	it, err := d.DB.Iterator(start, end)
	if err != nil {
		return nil, err
	}
	if d.failIteratorError {
		return &errorIterator{Iterator: it}, nil
	}
	return it, nil
}

// errorIterator wraps a dbm.Iterator and reports an error from Error() after
// iteration completes, mimicking a storage failure observed mid-scan.
type errorIterator struct {
	dbm.Iterator
}

func (it *errorIterator) Error() error {
	return errInjected
}

func indexedTxResult(events []abci.Event) *abci.TxResult {
	tx := types.Tx("HELLO WORLD")
	return &abci.TxResult{
		Height: 1,
		Index:  0,
		Tx:     tx,
		Result: abci.ExecTxResult{
			Data:   []byte{0},
			Code:   abci.CodeTypeOK,
			Log:    "",
			Events: events,
		},
	}
}

// newPopulatedStore returns a MemDB already holding one indexed tx so that the
// match/matchRange iterators have at least one row to scan.
func newPopulatedStore(t *testing.T) dbm.DB {
	t.Helper()

	store := dbm.NewMemDB()
	idx := NewTxIndex(store)
	txResult := indexedTxResult([]abci.Event{
		{Type: "account", Attributes: []abci.EventAttribute{{Key: "number", Value: "1", Index: true}}},
		{Type: "account", Attributes: []abci.EventAttribute{{Key: "owner", Value: "Ivan", Index: true}}},
	})
	require.NoError(t, idx.Index([]*abci.TxResult{txResult}))
	return store
}

// TestTxIndexGetStorageError asserts Get surfaces a storage read failure as an
// error instead of panicking.
func TestTxIndexGetStorageError(t *testing.T) {
	store := newPopulatedStore(t)
	idx := NewTxIndex(&errorDB{DB: store, failGet: true})

	hash := types.Tx("HELLO WORLD").Hash()

	require.NotPanics(t, func() {
		_, err := idx.Get(hash)
		require.Error(t, err)
		require.ErrorIs(t, err, errInjected)
	})
}

// TestTxIndexSearchStorageErrors drives Search through match, matchRange, and
// the hash-lookup Get path, asserting that both iterator-creation failures and
// post-iteration Iterator.Error() failures are returned, never panicked.
func TestTxIndexSearchStorageErrors(t *testing.T) {
	hash := types.Tx("HELLO WORLD").Hash()

	testCases := []struct {
		name string
		// q is the search query exercised against the decorated store.
		q string
		// mutate configures which failure the decorated store injects.
		mutate func(*errorDB)
	}{
		// match: c.Op == TEq
		{"match_equal_open", "account.number = 1", func(d *errorDB) { d.failIteratorOpen = true }},
		{"match_equal_iter_error", "account.number = 1", func(d *errorDB) { d.failIteratorError = true }},
		// match: c.Op == TExists
		{"match_exists_open", "account.number EXISTS", func(d *errorDB) { d.failIteratorOpen = true }},
		{"match_exists_iter_error", "account.number EXISTS", func(d *errorDB) { d.failIteratorError = true }},
		// match: c.Op == TContains
		{"match_contains_open", "account.owner CONTAINS 'an'", func(d *errorDB) { d.failIteratorOpen = true }},
		{"match_contains_iter_error", "account.owner CONTAINS 'an'", func(d *errorDB) { d.failIteratorError = true }},
		// matchRange
		{"match_range_open", "account.number >= 1 AND account.number <= 5", func(d *errorDB) { d.failIteratorOpen = true }},
		{"match_range_iter_error", "account.number >= 1 AND account.number <= 5", func(d *errorDB) { d.failIteratorError = true }},
		// Search hash-lookup path delegates to Get.
		{"search_hash_get_error", fmt.Sprintf("tx.hash = '%X'", hash), func(d *errorDB) { d.failGet = true }},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			store := newPopulatedStore(t)
			decorated := &errorDB{DB: store}
			tc.mutate(decorated)
			idx := NewTxIndex(decorated)

			require.NotPanics(t, func() {
				_, err := idx.Search(context.Background(), query.MustCompile(tc.q))
				require.Error(t, err)
				require.ErrorIs(t, err, errInjected)
			})
		})
	}
}
