package config

import (
	"context"
	"fmt"
	"path/filepath"

	dbm "github.com/cometbft/cometbft-db"

	"github.com/dashpay/tenderdash/libs/log"
	tmos "github.com/dashpay/tenderdash/libs/os"
	"github.com/dashpay/tenderdash/libs/service"
)

// ServiceProvider takes a config and a logger and returns a ready to go Node.
type ServiceProvider func(context.Context, *Config, log.Logger) (service.Service, error)

// DBContext specifies config information for loading a new DB.
type DBContext struct {
	ID     string
	Config *Config
}

// DBProvider takes a DBContext and returns an instantiated DB.
type DBProvider func(*DBContext) (dbm.DB, error)

// DefaultDBProvider returns a database using the DBBackend and DBDir
// specified in the Config.
func DefaultDBProvider(ctx *DBContext) (dbm.DB, error) {
	dbType := dbm.BackendType(ctx.Config.DBBackend)
	dbDir := ctx.Config.DBDir()

	// Check directory permissions before attempting to open the database
	if err := checkDBDirectory(dbDir); err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	db, err := dbm.NewDB(ctx.ID, dbType, dbDir)
	if err != nil {
		// Wrap with permission diagnostics if it's a permission error
		dbPath := filepath.Join(dbDir, ctx.ID+".db")
		err = tmos.WrapPermissionError(dbPath, "open database", err)
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	return db, nil
}

// checkDBDirectory verifies the database directory exists and is accessible
func checkDBDirectory(dbDir string) error {
	// Check if directory exists and is accessible
	if err := tmos.CheckFileAccess(dbDir, "access database directory"); err != nil {
		return err
	}

	// Check if directory is writable
	if err := tmos.CheckDirectoryWritable(dbDir); err != nil {
		return err
	}

	return nil
}
