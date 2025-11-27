package cmd

import (
	"github.com/mattsolo1/grove-core/cli"
	"github.com/spf13/cobra"
)

// NewRootCmd creates the root command for aglogs.
func NewRootCmd() *cobra.Command {
	rootCmd := cli.NewStandardCommand(
		"aglogs",
		"Agent transcript log parsing and monitoring",
	)

	rootCmd.AddCommand(newListCmd())
	rootCmd.AddCommand(newTailCmd())
	rootCmd.AddCommand(newQueryCmd())
	rootCmd.AddCommand(newReadCmd())
	rootCmd.AddCommand(newGetSessionInfoCmd())
	rootCmd.AddCommand(NewVersionCmd())

	return rootCmd
}
