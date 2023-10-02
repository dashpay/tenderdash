package main

import (
	"context"
	"os"

	"github.com/dashpay/tenderdash/cmd/tenderdash/commands"
	"github.com/dashpay/tenderdash/cmd/tenderdash/commands/debug"
	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/libs/cli"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/node"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conf, err := commands.ParseConfig(config.DefaultConfig())
	if err != nil {
		panic(err)
	}

	logger, err := log.NewMultiLogger(conf.LogFormat, conf.LogLevel, conf.LogFilePath)
	if err != nil {
		panic(err)
	}
	defer logger.Close()

	rcmd := commands.RootCommand(conf, logger)
	rcmd.AddCommand(
		commands.MakeGenValidatorCommand(),
		commands.MakeReindexEventCommand(conf, logger),
		commands.MakeInitFilesCommand(conf, logger),
		commands.MakeLightCommand(conf, logger),
		commands.MakeReplayCommand(conf, logger),
		commands.MakeReplayConsoleCommand(conf, logger),
		commands.MakeShowValidatorCommand(conf, logger),
		commands.MakeTestnetFilesCommand(conf, logger),
		commands.MakeShowNodeIDCommand(conf),
		commands.MakeGenNodeKeyCommand(conf, logger),
		commands.VersionCmd,
		commands.MakeInspectCommand(conf, logger),
		commands.MakeRollbackStateCommand(conf),
		commands.MakeKeyMigrateCommand(conf, logger),
		debug.GetDebugCommand(logger),
		commands.NewCompletionCmd(rcmd, true),
		commands.MakeCompactDBCommand(conf, logger),
	)

	// NOTE:
	// Users wishing to:
	//	* Use an external signer for their validators
	//	* Supply an in-proc abci app
	//	* Supply a genesis doc file from another source
	//	* Provide their own DB implementation
	// can copy this file and use something other than the
	// node.NewDefault function
	nodeFunc := node.NewDefault

	// Create & start node
	rcmd.AddCommand(commands.NewRunNodeCmd(nodeFunc, conf, logger))

	if err := cli.RunWithTrace(ctx, rcmd); err != nil {
		// os.Exit doesn't call defer functions, so we manually close the logger here
		cancel()
		_ = logger.Close()
		os.Exit(2)
	}
}
