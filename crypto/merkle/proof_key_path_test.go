package merkle

import (
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/exp/rand"
)

func TestKeyPath(t *testing.T) {
	var path KeyPath
	keys := make([][]byte, 10)
	alphanum := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

	for d := 0; d < 1e4; d++ {
		path = nil

		for i := range keys {
			enc := keyEncoding(rand.Intn(int(KeyEncodingMax)))
			keys[i] = make([]byte, rand.Uint32()%20)
			switch enc {
			case KeyEncodingURL:
				for j := range keys[i] {
					keys[i][j] = alphanum[rand.Intn(len(alphanum))]
				}
			case KeyEncodingHex:
				_, _ = rand.Read(keys[i])
			default:
				require.Fail(t, "Unexpected encoding")
			}
			path = path.AppendKey(keys[i], enc)
		}

		res, err := KeyPathToKeys(path.String())
		require.NoError(t, err)
		require.Equal(t, len(keys), len(res))

		for i, key := range keys {
			require.Equal(t, key, res[i])
		}
	}
}
