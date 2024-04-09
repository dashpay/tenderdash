package commands

import (
	"github.com/spf13/cobra"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/consensus"
	"github.com/dashpay/tenderdash/libs/log"
)

// MakeReplayCommand constructs a command to replay messages from the WAL into consensus.
func MakeReplayCommand(conf *config.Config, logger log.Logger) *cobra.Command {
	return &cobra.Command{
		Use:   "replay",
		Short: "Replay messages from WAL",
		RunE: func(cmd *cobra.Command, _args []string) error {
			return consensus.RunReplayFile(cmd.Context(), logger, *conf, conf.Consensus, false)
		},
	}
}

// MakeReplayConsoleCommand constructs a command to replay WAL messages to stdout.
func MakeReplayConsoleCommand(conf *config.Config, logger log.Logger) *cobra.Command {
	return &cobra.Command{
		Use:   "replay-console",
		Short: "Replay messages from WAL in a console",
		RunE: func(cmd *cobra.Command, _args []string) error {
			return consensus.RunReplayFile(cmd.Context(), logger, *conf, conf.Consensus, true)
		},
	}
}
