package commands

import (
	"github.com/spf13/cobra"
)

// RootCmd is the root command for Tendermint core.
var RootCmd = cobra.Command{
	Use:   "gossipviz",
	Short: "Visualize tenderdash gossip protocol operations",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) (err error) {
		return nil
	},
}
