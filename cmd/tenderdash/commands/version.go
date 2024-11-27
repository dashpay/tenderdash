package commands

import (
	"runtime"

	"github.com/spf13/cobra"

	"github.com/dashpay/tenderdash/version"
)

// VersionCmd ...
var VersionCmd *cobra.Command = func() *cobra.Command {
	verbose := false
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show version info",

		Run: func(cmd *cobra.Command, _ []string) {
			cmd.Println(version.TMCoreSemVer)
			if verbose {
				cmd.Println("Go version: " + runtime.Version())
			}
		},
	}
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "display additional compilation info")
	return cmd
}()
