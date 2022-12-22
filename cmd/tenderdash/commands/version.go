package commands

import (
	"fmt"
	"runtime"

	"github.com/sasha-s/go-deadlock"
	"github.com/spf13/cobra"

	"github.com/tendermint/tendermint/version"
)

// VersionCmd ...
var VersionCmd *cobra.Command = func() *cobra.Command {
	verbose := false
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show version info",

		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(version.TMCoreSemVer)
			if verbose {
				fmt.Println("Go version: " + runtime.Version())
				if deadlock.Opts.Disable {
					fmt.Println("Deadlock detection: disabled")
				} else {
					fmt.Println("Deadlock detection: enabled, timeout: ", deadlock.Opts.DeadlockTimeout.String())
				}
			}
		},
	}
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "display additional compilation info")
	return cmd
}()
