package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tendermint/tendermint/libs/log"
)

var logger log.Logger

// MakeRootCmd constructs the root command-line entry point
func MakeRootCmd() *cobra.Command {
	var (
		logLevel string
	)

	cmd := &cobra.Command{
		Use:   "abcidump",
		Short: "Parse dump of protobuf communication between two nodes",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) (err error) {
			logger, err = log.NewDefaultLogger(log.LogFormatText, logLevel, false)
			if err != nil {
				return fmt.Errorf("log-level: %w", err)
			}
			logger = logger.Output(cmd.OutOrStderr())
			logger = logger.With("module", "main")
			return nil
		},
	}
	cmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level")

	return cmd
}
