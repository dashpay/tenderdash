package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigRejectsPostgreSQLIndexer(t *testing.T) {
	for _, indexers := range [][]string{
		{"psql"}, {"PSQL"}, {"kv", "psql"}, {"null", "psql"}, {"psql", "null"},
	} {
		t.Run(indexers[0]+"_"+indexers[len(indexers)-1], func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.TxIndex.Indexer = indexers
			err := cfg.ValidateBasic()
			require.ErrorContains(t, err, "PostgreSQL event indexing has been removed")
			require.ErrorContains(t, err, `"kv"`)
			require.ErrorContains(t, err, `"null"`)
		})
	}
}
