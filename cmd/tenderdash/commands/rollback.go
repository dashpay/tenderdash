package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/state"
)

func MakeRollbackStateCommand(conf *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rollback",
		Short: "rollback tendermint state by one height",
		Long: `
A state rollback is performed to recover from an incorrect application state transition,
when Tendermint has persisted an incorrect app hash and is thus unable to make
progress. Rollback overwrites a state at height n with the state at height n - 1.
The application should also roll back to height n - 1. No blocks are removed, so upon
restarting Tendermint the transactions in block n will be re-executed against the
application.

If the --store flag is set, the block store will also be rolled back to match the state height.
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			storeRollback, _ := cmd.Flags().GetBool("store")
			height, hash, err := RollbackState(conf, storeRollback)
			if err != nil {
				return fmt.Errorf("failed to rollback state: %w", err)
			}

			fmt.Printf("Rolled back state to height %d and hash %X", height, hash)
			return nil
		},
	}

	cmd.Flags().Bool("store", false, "also roll back the block store to match the state height")
	return cmd
}

// RollbackState takes the state at the current height n and overwrites it with the state
// at height n - 1. Note state here refers to tendermint state not application state.
// Returns the latest state height and app hash alongside an error if there was one.
func RollbackState(config *config.Config, rollbackStore bool) (int64, []byte, error) {
	// use the parsed config to load the block and state store
	blockStore, stateStore, err := loadStateAndBlockStore(config)
	if err != nil {
		return -1, nil, err
	}
	defer func() {
		_ = blockStore.Close()
		_ = stateStore.Close()
	}()

	// rollback the last state
	height, hash, err := state.Rollback(blockStore, stateStore)
	if err != nil {
		return -1, nil, err
	}

	if rollbackStore {
		// Rollback the block store to match the state height
		for currentHeight := blockStore.Height(); currentHeight > height; currentHeight-- {
			_, err := blockStore.DeleteBlock(currentHeight)
			if err != nil {
				return -1, nil, fmt.Errorf("failed to delete block at height %d: %w", currentHeight, err)
			}
		}
	}

	return height, hash, nil
}
