package sink

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/libs/db"
)

func TestPostgreSQLRejectedBeforeOpeningSinks(t *testing.T) {
	for _, indexers := range [][]string{
		{"psql"}, {"PSQL"}, {"kv", "psql"}, {"null", "psql"}, {"psql", "null"},
	} {
		t.Run(indexers[0]+"_"+indexers[len(indexers)-1], func(t *testing.T) {
			cfg := config.TestConfig()
			cfg.TxIndex.Indexer = indexers
			provider := func(*config.DBContext) (db.DB, error) {
				t.Fatal("invalid indexer selection must be rejected before opening a database")
				return nil, nil
			}
			sinks, err := EventSinksFromConfig(cfg, provider, "test-chain")
			require.ErrorContains(t, err, "PostgreSQL event indexing has been removed")
			require.Nil(t, sinks)
		})
	}
}
