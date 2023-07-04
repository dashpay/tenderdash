package kvstore

import (
	"encoding/hex"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChunkItem(t *testing.T) {
	const size = 64
	chunks := makeChunks(makeBytes(1032), size)
	keys := chunks.Keys()
	values := chunks.Values()
	for i, key := range keys {
		chunkID, err := hex.DecodeString(key)
		require.NoError(t, err)
		item := makeChunkItem(chunks, chunkID)
		require.Equal(t, values[i], item.Data)
		if i+1 < len(keys) {
			nextChunkID, err := hex.DecodeString(keys[i+1])
			require.NoError(t, err)
			require.Equal(t, [][]byte{nextChunkID}, item.NextChunkIDs)
		} else {
			require.Nil(t, item.NextChunkIDs)
		}
	}
}

func makeBytes(size int) []byte {
	bz := make([]byte, size)
	for i := 0; i < size; i++ {
		bz[i] = byte(rand.Int63n(256))
	}
	return bz
}
