package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	grovelogging "github.com/grovetools/core/logging"
	"github.com/spf13/cobra"

	"github.com/grovetools/agentlogs/internal/opencode"
	"github.com/grovetools/agentlogs/internal/session"
	"github.com/grovetools/agentlogs/pkg/transcript"
)

var ulogQuery = grovelogging.NewUnifiedLogger("grove-agent-logs.cmd.query")

func newQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query <session_id>",
		Short: "Query messages from a transcript",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			role, _ := cmd.Flags().GetString("role")
			jsonOutput, _ := cmd.Flags().GetBool("json")

			// The historical Claude path-glob lookup runs first, unchanged;
			// only when it misses is the tiered multi-provider resolver
			// consulted (codex/pi/opencode session ids, flow job ids).
			provider := "claude"
			transcriptPath, err := transcript.GetTranscriptPathLegacy(sessionID)
			if err != nil {
				info, rerr := session.ResolveSessionInfo(sessionID)
				if rerr != nil || info.LogFilePath == "" {
					return fmt.Errorf("failed to find transcript: %w", err)
				}
				transcriptPath = info.LogFilePath
				if info.Provider != "" {
					provider = info.Provider
				}
			}

			messages, err := queryMessages(transcriptPath, provider)
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
					Emit()
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
					Emit()

				for _, msg := range filtered {
					ulogQuery.Info("Message").
						Field("session_id", sessionID).
						Field("message_id", msg.MessageID).
						Field("role", msg.Role).
						Field("timestamp", msg.Timestamp).
						Pretty(fmt.Sprintf("[%s] %s: %s\n", msg.Timestamp.Format("15:04:05"), msg.Role, msg.Content)).
						PrettyOnly().
						Emit()
				}
			}

			return nil
		},
	}

	cmd.Flags().String("role", "", "Filter by message role (user, assistant)")
	cmd.Flags().Bool("json", false, "Output in JSON format")

	return cmd
}

// queryMessages extracts the messages of a resolved transcript, routed by
// provider. Claude keeps the historical Parser.ParseFile chain; codex uses
// the codex-shaped parser; pi and opencode go through their normalizers
// (linearized active branch for pi, fragment assembly for opencode — path is
// the session info file there) and flatten to the same ExtractedMessage
// shape.
func queryMessages(path, provider string) ([]transcript.ExtractedMessage, error) {
	switch provider {
	case "codex":
		parser := transcript.NewParser()
		messages, _, err := parser.ParseCodexFileFromOffset(path, 0)
		return messages, err
	case "pi":
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		entries, err := transcript.NormalizePiFile(f)
		if err != nil {
			return nil, err
		}
		return extractedFromUnified(entries), nil
	case "opencode":
		return opencodeQueryMessages(path)
	default:
		parser := transcript.NewParser()
		return parser.ParseFile(path)
	}
}

// extractedFromUnified flattens normalized entries into ExtractedMessages
// (text parts joined; entries with no text are skipped).
func extractedFromUnified(entries []transcript.UnifiedEntry) []transcript.ExtractedMessage {
	var out []transcript.ExtractedMessage
	for _, e := range entries {
		var texts []string
		for _, p := range e.Parts {
			if tc, ok := p.Content.(transcript.UnifiedTextContent); ok && tc.Text != "" {
				texts = append(texts, tc.Text)
			}
		}
		if len(texts) == 0 {
			continue
		}
		out = append(out, transcript.ExtractedMessage{
			MessageID: e.MessageID,
			Timestamp: e.Timestamp,
			Role:      e.Role,
			Content:   strings.Join(texts, "\n"),
		})
	}
	return out
}

// opencodeQueryMessages assembles an opencode session's messages from its
// session info file path (<storage>/session/<projectID>/<ses_...>.json).
func opencodeQueryMessages(path string) ([]transcript.ExtractedMessage, error) {
	sessionID := strings.TrimSuffix(filepath.Base(path), ".json")
	// <storage>/session/<projectID>/<ses_...>.json -> <storage>
	storageDir := filepath.Dir(filepath.Dir(filepath.Dir(path)))
	assembler, err := opencode.NewAssemblerWithDir(storageDir)
	if err != nil {
		return nil, err
	}
	entries, err := assembler.AssembleTranscript(sessionID)
	if err != nil {
		return nil, err
	}
	var out []transcript.ExtractedMessage
	for _, e := range entries {
		var texts []string
		for _, p := range e.Parts {
			if tp, ok := p.Content.(opencode.TextPart); ok && tp.Text != "" {
				texts = append(texts, tp.Text)
			}
		}
		if len(texts) == 0 {
			continue
		}
		out = append(out, transcript.ExtractedMessage{
			SessionID: sessionID,
			MessageID: e.MessageID,
			Timestamp: e.Timestamp,
			Role:      e.Role,
			Content:   strings.Join(texts, "\n"),
		})
	}
	return out, nil
}
