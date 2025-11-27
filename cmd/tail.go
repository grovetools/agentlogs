package cmd

import (
	"fmt"

	"github.com/mattsolo1/grove-agent-logs/internal/transcript"
	"github.com/spf13/cobra"
)

func newTailCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tail <session_id>",
		Short: "Tail and parse messages from a specific transcript",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]

			transcriptPath, err := transcript.GetTranscriptPathLegacy(sessionID)
			if err != nil {
				return fmt.Errorf("failed to find transcript: %w", err)
			}

			parser := transcript.NewParser()
			messages, err := parser.ParseFile(transcriptPath)
			if err != nil {
				return fmt.Errorf("failed to parse transcript: %w", err)
			}

			start := 0
			if len(messages) > 10 {
				start = len(messages) - 10
			}

			fmt.Printf("Showing last %d messages from session %s:\n\n", len(messages)-start, sessionID)

			for i := start; i < len(messages); i++ {
				msg := messages[i]
				fmt.Printf("[%s] %s: %s\n\n", msg.Timestamp.Format("15:04:05"), msg.Role, msg.Content)
			}

			return nil
		},
	}

	return cmd
}
