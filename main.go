package main

import (
	"os"
	"github.com/mattsolo1/grove-core/cli"
	"github.com/spf13/cobra"
)

func main() {
	rootCmd := cli.NewStandardCommand(
		"clogs",
		"Claude transcript log parsing and monitoring",
	)
	
	// Add subcommands
	rootCmd.AddCommand(newListCmd())
	rootCmd.AddCommand(newTailCmd())
	rootCmd.AddCommand(newQueryCmd())
	
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available session transcripts",
		Run: func(cmd *cobra.Command, args []string) {
			// TODO: Implement list functionality
			cmd.Println("Listing available session transcripts...")
		},
	}
	
	return cmd
}

func newTailCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tail <session_id>",
		Short: "Tail and parse messages from a specific transcript",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			// TODO: Implement tail functionality
			sessionID := args[0]
			cmd.Printf("Tailing transcript for session: %s\n", sessionID)
		},
	}
	
	return cmd
}

func newQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query <session_id>",
		Short: "Query messages from a transcript",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			// TODO: Implement query functionality
			sessionID := args[0]
			role, _ := cmd.Flags().GetString("role")
			cmd.Printf("Querying transcript for session: %s, role: %s\n", sessionID, role)
		},
	}
	
	cmd.Flags().String("role", "", "Filter by message role (user, assistant)")
	
	return cmd
}