package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mattsolo1/grove-agent-logs/internal/session"
	grovelogging "github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/pkg/sessions"
	"github.com/spf13/cobra"
)

var ulogGetSessionInfo = grovelogging.NewUnifiedLogger("grove-agent-logs.cmd.getSessionInfo")

func newGetSessionInfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "get-session-info <job-file>",
		Short:  "Get session details for a given job file",
		Long:   "Retrieves the native agent session ID and provider for a given Grove job file path from the sessions database or transcript logs.",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobFilePath := args[0]

			parts := strings.Split(jobFilePath, string(filepath.Separator))
			if len(parts) < 2 {
				return fmt.Errorf("invalid job file path format: %s", jobFilePath)
			}
			jobFilename := parts[len(parts)-1]
			planName := parts[len(parts)-2]

			var agentSessionID, provider string

			if content, err := os.ReadFile(jobFilePath); err == nil {
				idRegex := regexp.MustCompile(`(?m)^id:\s*(.+)$`)
				if matches := idRegex.FindStringSubmatch(string(content)); len(matches) > 1 {
					jobID := strings.TrimSpace(matches[1])

					registry, err := sessions.NewFileSystemRegistry()
					if err == nil {
						session, err := registry.Find(jobID)
						if err == nil && session.ClaudeSessionID != "" {
							agentSessionID = session.ClaudeSessionID
							provider = session.Provider
						}
					}
				}
			}

			if agentSessionID == "" {
				scanner := session.NewScanner()
				allSessions, err := scanner.Scan()
				if err != nil {
					return fmt.Errorf("failed to scan for sessions: %w", err)
				}

				for _, s := range allSessions {
					for _, job := range s.Jobs {
						if job.Plan == planName && job.Job == jobFilename {
							agentSessionID = s.SessionID
							if strings.Contains(s.LogFilePath, "/.codex/") {
								provider = "codex"
							} else {
								provider = "claude"
							}
							break
						}
					}
					if agentSessionID != "" {
						break
					}
				}

				if agentSessionID == "" {
					return fmt.Errorf("could not find session for job %s/%s in registry or transcript logs", planName, jobFilename)
				}
			}

			output := struct {
				AgentSessionID string `json:"agent_session_id"`
				Provider       string `json:"provider"`
			}{
				AgentSessionID: agentSessionID,
				Provider:       provider,
			}

			jsonData, err := json.Marshal(output)
			if err != nil {
				return fmt.Errorf("failed to marshal session info to JSON: %w", err)
			}

			ulogGetSessionInfo.Info("Session info retrieved").
				Field("agent_session_id", agentSessionID).
				Field("provider", provider).
				Field("plan", planName).
				Field("job", jobFilename).
				Pretty(string(jsonData)).
				PrettyOnly().
				Emit()
			return nil
		},
	}
	return cmd
}
