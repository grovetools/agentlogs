package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/mattsolo1/grove-agent-logs/internal/transcript"
	"github.com/spf13/cobra"
)

func newQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query <session_id>",
		Short: "Query messages from a transcript",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			role, _ := cmd.Flags().GetString("role")
			jsonOutput, _ := cmd.Flags().GetBool("json")

			transcriptPath, err := transcript.GetTranscriptPathLegacy(sessionID)
			if err != nil {
				return fmt.Errorf("failed to find transcript: %w", err)
			}

			parser := transcript.NewParser()
			messages, err := parser.ParseFile(transcriptPath)
			if err != nil {
				return fmt.Errorf("failed to parse transcript: %w", err)
			}

			var filtered []transcript.ExtractedMessage
			for _, msg := range messages {
				if role == "" || msg.Role == role {
					filtered = append(filtered, msg)
				}
			}

			if jsonOutput {
				data, err := json.MarshalIndent(filtered, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to marshal messages: %w", err)
				}
				fmt.Println(string(data))
			} else {
				fmt.Printf("Found %d messages", len(filtered))
				if role != "" {
					fmt.Printf(" with role '%s'", role)
				}
				fmt.Printf(" in session %s:\n\n", sessionID)

				for _, msg := range filtered {
					fmt.Printf("[%s] %s: %s\n\n", msg.Timestamp.Format("15:04:05"), msg.Role, msg.Content)
				}
			}

			return nil
		},
	}

	cmd.Flags().String("role", "", "Filter by message role (user, assistant)")
	cmd.Flags().Bool("json", false, "Output in JSON format")

	return cmd
}
