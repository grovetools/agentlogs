package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mattsolo1/grove-agent-logs/internal/transcript"
	grovelogging "github.com/mattsolo1/grove-core/logging"
	"github.com/spf13/cobra"
)

var ulogQuery = grovelogging.NewUnifiedLogger("grove-agent-logs.cmd.query")

func newQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query <session_id>",
		Short: "Query messages from a transcript",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
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
				ulogQuery.Info("Query results").
					Field("message_count", len(filtered)).
					Field("session_id", sessionID).
					Field("role_filter", role).
					Pretty(string(data)).
					PrettyOnly().
					Log(ctx)
			} else {
				// Build summary message
				summaryMsg := fmt.Sprintf("Found %d messages", len(filtered))
				if role != "" {
					summaryMsg += fmt.Sprintf(" with role '%s'", role)
				}
				summaryMsg += fmt.Sprintf(" in session %s:\n\n", sessionID)

				ulogQuery.Info("Query results").
					Field("message_count", len(filtered)).
					Field("session_id", sessionID).
					Field("role_filter", role).
					Pretty(summaryMsg).
					PrettyOnly().
					Log(ctx)

				for _, msg := range filtered {
					ulogQuery.Info("Message").
						Field("session_id", sessionID).
						Field("message_id", msg.MessageID).
						Field("role", msg.Role).
						Field("timestamp", msg.Timestamp).
						Pretty(fmt.Sprintf("[%s] %s: %s\n", msg.Timestamp.Format("15:04:05"), msg.Role, msg.Content)).
						PrettyOnly().
						Log(ctx)
				}
			}

			return nil
		},
	}

	cmd.Flags().String("role", "", "Filter by message role (user, assistant)")
	cmd.Flags().Bool("json", false, "Output in JSON format")

	return cmd
}
