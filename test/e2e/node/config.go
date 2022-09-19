//nolint: goconst
package main

import (
	"errors"
	"fmt"

	"github.com/BurntSushi/toml"

	"github.com/tendermint/tendermint/abci/example/kvstore"
)

// Config is the application configuration.
type Config struct {
	ChainID          string `toml:"chain_id"`
	Listen           string
	Protocol         string
	Dir              string
	Mode             string                       `toml:"mode"`
	PersistInterval  uint64                       `toml:"persist_interval"`
	SnapshotInterval uint64                       `toml:"snapshot_interval"`
	RetainBlocks     int64                        `toml:"retain_blocks"`
	ValidatorUpdates map[string]map[string]string `toml:"validator_update"`
	PrivValServer    string                       `toml:"privval_server"`
	PrivValKey       string                       `toml:"privval_key"`
	PrivValState     string                       `toml:"privval_state"`
	KeyType          string                       `toml:"key_type"`

	// dash parameters
	ThesholdPublicKeyUpdate map[string]string `toml:"threshold_public_key_update"`
	QuorumHashUpdate        map[string]string `toml:"quorum_hash_update"`
	ChainLockUpdates        map[string]string `toml:"chainlock_updates"`
	PrivValServerType       string            `toml:"privval_server_type"`
}

// App extracts out the application specific configuration parameters
func (cfg *Config) App() *kvstore.Config {
	return &kvstore.Config{
		Dir:              cfg.Dir,
		SnapshotInterval: cfg.SnapshotInterval,
		RetainBlocks:     cfg.RetainBlocks,
		KeyType:          cfg.KeyType,
		ValidatorUpdates: cfg.ValidatorUpdates,
		PersistInterval:  cfg.PersistInterval,
		// dash params
		ThesholdPublicKeyUpdate: cfg.ThesholdPublicKeyUpdate,
		QuorumHashUpdate:        cfg.QuorumHashUpdate,
		ChainLockUpdates:        cfg.ChainLockUpdates,
		PrivValServerType:       cfg.PrivValServerType,
	}
}

// LoadConfig loads the configuration from disk.
func LoadConfig(file string) (*Config, error) {
	cfg := &Config{
		Listen:          "unix:///var/run/app.sock",
		Protocol:        "socket",
		PersistInterval: 1,
	}
	_, err := toml.DecodeFile(file, &cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to load config from %q: %w", file, err)
	}
	return cfg, cfg.Validate()
}

// Validate validates the configuration. We don't do exhaustive config
// validation here, instead relying on Testnet.Validate() to handle it.
func (cfg Config) Validate() error {
	switch {
	case cfg.ChainID == "":
		return errors.New("chain_id parameter is required")
	case cfg.Listen == "" && cfg.Protocol != "builtin":
		return errors.New("listen parameter is required")
	default:
		return nil
	}
}
