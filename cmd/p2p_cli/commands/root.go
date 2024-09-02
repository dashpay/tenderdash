package commands

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/dashpay/tenderdash/libs/log"
)

var (
	logLevel string
	logger   log.Logger
)

type Cmd interface {
	Command() *cobra.Command
}

func NewRootCmd(commands ...*cobra.Command) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:               "app",
		Short:             "A CLI application",
		PersistentPreRunE: rootPreRunE,
	}

	rootCmd.PersistentFlags().StringVarP(&logLevel, "log-level", "l", "silent", "Enable logging on selected level (info, debug, trace, error); defaults to silent")

	rootCmd.AddCommand(newVersionCmd(logger).Command())

	return rootCmd
}

func rootPreRunE(cmd *cobra.Command, _args []string) error {
	var err error
	switch logLevel {
	case "info":
		logger, err = log.NewLogger(log.LogLevelInfo, os.Stdout)
	case "debug":
		logger, err = log.NewLogger(log.LogLevelDebug, os.Stdout)
	case "trace":
		logger, err = log.NewLogger(log.LogLevelTrace, os.Stdout)
	case "error":
		logger, err = log.NewLogger(log.LogLevelError, os.Stdout)
	default:
		logger = log.NewNopLogger()
	}

	if err != nil {
		return err
	}

	return nil
}
