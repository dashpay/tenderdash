package mempool

import (
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/require"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/types"
)

func TestCacheRemove(t *testing.T) {
	cache := NewLRUTxCache(100)
	numTxs := 10

	txs := make([][]byte, numTxs)
	for i := 0; i < numTxs; i++ {
		// probability of collision is 2**-256
		txBytes := make([]byte, 32)
		_, err := rand.Read(txBytes)
		require.NoError(t, err)

		txs[i] = txBytes
		cache.Push(txBytes)

		// make sure its added to both the linked list and the map
		require.Equal(t, i+1, len(cache.cacheMap))
		require.Equal(t, i+1, cache.list.Len())
	}

	for i := 0; i < numTxs; i++ {
		cache.Remove(txs[i])
		// make sure its removed from both the map and the linked list
		require.Equal(t, numTxs-(i+1), len(cache.cacheMap))
		require.Equal(t, numTxs-(i+1), cache.list.Len())
	}
}

func TestCacheRejection(t *testing.T) {
	rejected := &abci.ResponseCheckTx{
		Code:      7,
		Codespace: "app",
		Info:      "insufficient balance",
		Data:      []byte{0xde, 0xad},
		GasWanted: 1,
		Sender:    "sender",
		Priority:  5,
	}
	want := &abci.ResponseCheckTx{Code: 7, Codespace: "app", Info: "insufficient balance", Data: []byte{0xde, 0xad}}
	tx, other := types.Tx("tx"), types.Tx("other")

	testCases := []struct {
		name string
		run  func(cache *LRUTxCache)
		want *abci.ResponseCheckTx
	}{
		{
			name: "stored for a cached transaction",
			run:  func(*LRUTxCache) {},
			want: want,
		},
		{
			name: "returned copy does not alias the stored one",
			run: func(cache *LRUTxCache) {
				got := cache.Rejection(tx)
				got.Code, got.Data[0] = 99, 0xff
			},
			want: want,
		},
		{
			name: "stored copy does not alias the caller's response",
			run:  func(*LRUTxCache) { rejected.Data[0] = 0xff },
			want: want,
		},
		{
			name: "cleared with nil",
			run:  func(cache *LRUTxCache) { cache.SetRejection(tx, nil) },
		},
		{
			name: "dropped when replaced by an oversized response",
			run: func(cache *LRUTxCache) {
				cache.SetRejection(tx, &abci.ResponseCheckTx{Code: 8, Data: make([]byte, maxCachedRejectionBytes+1)})
			},
		},
		{
			name: "dropped on remove",
			run:  func(cache *LRUTxCache) { cache.Remove(tx) },
		},
		{
			name: "dropped on reset",
			run:  func(cache *LRUTxCache) { cache.Reset() },
		},
		{
			name: "dropped on eviction",
			run: func(cache *LRUTxCache) {
				cache.Push(other)
				cache.Push(types.Tx("third"))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cache := NewLRUTxCache(2)
			cache.Push(tx)
			cache.SetRejection(tx, rejected)

			tc.run(cache)
			rejected.Data[0] = 0xde

			require.Equal(t, tc.want, cache.Rejection(tx))
			if tc.want == nil {
				require.Empty(t, cache.rejections)
			}
		})
	}

	t.Run("ignored for a transaction that is not cached", func(t *testing.T) {
		cache := NewLRUTxCache(2)
		cache.SetRejection(tx, rejected)

		require.Nil(t, cache.Rejection(tx))
		require.Empty(t, cache.rejections)
	})

	t.Run("largest allowed response is stored", func(t *testing.T) {
		cache := NewLRUTxCache(2)
		cache.Push(tx)
		cache.SetRejection(tx, &abci.ResponseCheckTx{Code: 8, Data: make([]byte, maxCachedRejectionBytes)})

		require.NotNil(t, cache.Rejection(tx))
	})
}
