package cmd

import (
	"fmt"

	"github.com/grovetools/agentlogs/internal/transcript"
	grovelogging "github.com/grovetools/core/logging"
	"github.com/spf13/cobra"
)

var ulogTail = grovelogging.NewUnifiedLogger("grove-agent-logs.cmd.tail")

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

			ulogTail.Info("Tail messages").
				Field("session_id", sessionID).
				Field("message_count", len(messages)-start).
				Field("total_messages", len(messages)).
				Pretty(fmt.Sprintf("Showing last %d messages from session %s:\n\n", len(messages)-start, sessionID)).
				PrettyOnly().
				Emit()

			for i := start; i < len(messages); i++ {
				msg := messages[i]
				ulogTail.Info("Message").
					Field("session_id", sessionID).
					Field("message_id", msg.MessageID).
					Field("role", msg.Role).
					Field("timestamp", msg.Timestamp).
					Pretty(fmt.Sprintf("[%s] %s: %s\n", msg.Timestamp.Format("15:04:05"), msg.Role, msg.Content)).
					PrettyOnly().
					Emit()
			}

			return nil
		},
	}

	return cmd
}
