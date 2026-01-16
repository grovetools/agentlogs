package cmd

import (
	"github.com/grovetools/core/cli"
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
	rootCmd.AddCommand(newStreamCmd())
	rootCmd.AddCommand(NewVersionCmd())

	return rootCmd
}
