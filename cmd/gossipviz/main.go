package main

import (
	"os"

	cmd "github.com/tendermint/tendermint/cmd/gossipviz/commands"
	"github.com/tendermint/tendermint/libs/cli"
)

func main() {
	rootCmd := cmd.RootCmd
	rootCmd.AddCommand(cmd.VoteCmd())

	cmd := cli.PrepareBaseCmd(&rootCmd, "TM", os.ExpandEnv("$HOME"))
	if err := cmd.Execute(); err != nil {
		panic(err)
	}
}
