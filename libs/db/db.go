package db

import (
	"fmt"
	"strings"
)

type BackendType string

// These are valid backend types.
const (
	// GoLevelDBBackend is the persistent GoLevelDB backend.
	GoLevelDBBackend BackendType = "goleveldb"
	// MemDBBackend is an in-memory key-value store.
	MemDBBackend BackendType = "memdb"
)

type dbCreator func(name string, dir string) (DB, error)

var backends = map[BackendType]dbCreator{}

func registerDBCreator(backend BackendType, creator dbCreator) {
	_, ok := backends[backend]
	if ok {
		return
	}
	backends[backend] = creator
}

// NewDB creates a new database of type backend with the given name.
func NewDB(name string, backend BackendType, dir string) (DB, error) {
	dbCreator, ok := backends[backend]
	if !ok {
		keys := make([]string, 0, len(backends))
		for k := range backends {
			keys = append(keys, string(k))
		}
		return nil, fmt.Errorf("unknown db_backend %s, expected one of %v",
			backend, strings.Join(keys, ","))
	}

	db, err := dbCreator(name, dir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}
	return db, nil
}
