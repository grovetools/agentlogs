package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/grovetools/agentlogs/internal/display"
	"github.com/grovetools/agentlogs/internal/formatters"
	"github.com/grovetools/agentlogs/internal/provider"
	"github.com/grovetools/agentlogs/internal/session"
	grovelogging "github.com/grovetools/core/logging"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/spf13/cobra"
)

// isLogFilePath returns true if the spec looks like a direct log file path
// rather than a plan/job spec. This prevents plan markdown files from being
// accidentally matched by os.Stat when the cwd happens to be the plans directory.
func isLogFilePath(spec string) bool {
	// Absolute paths are always treated as file paths
	if filepath.IsAbs(spec) {
		_, err := os.Stat(spec)
		return err == nil
	}
	// Relative paths must have a log-like extension to be treated as file paths
	ext := filepath.Ext(spec)
	if ext == ".jsonl" || ext == ".log" {
		_, err := os.Stat(spec)
		return err == nil
	}
	return false
}

var ulogStream = grovelogging.NewUnifiedLogger("grove-agent-logs.cmd.stream")

func newStreamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "stream <spec>",
		Short:  "Stream logs for a specific job, session, or log file",
		Long:   "Finds and tails the agent transcript log. <spec> can be a plan/job, a session ID, or a direct path to a log file.",
		Args:   cobra.ExactArgs(1),
		Hidden: true, // Internal command for now
		RunE: func(cmd *cobra.Command, args []string) error {
			spec := args[0]
			jsonOutput, _ := cmd.Flags().GetBool("json")

			var sessionInfo *session.SessionInfo
			var err error

			// Fast path: if spec is an actual log file path (not a plan/job spec),
			// stream it directly. Plan/job specs like "plan/job.md" can match
			// os.Stat if the cwd is the plans directory, so we require the path
			// to look like a log file (absolute path, or .jsonl/.log extension).
			if isLogFilePath(spec) {
				prov := "claude"
				if strings.Contains(spec, "/.codex/") {
					prov = "codex"
				}
				sessionInfo = &session.SessionInfo{
					LogFilePath: spec,
					Provider:    prov,
				}
			} else {
				// Slow path: resolve session from spec with retries for newly started jobs
				sessionInfo, err = session.ResolveSessionInfo(spec)
				if err != nil {
					maxRetries := 5
					for attempt := 0; attempt < maxRetries && err != nil; attempt++ {
						time.Sleep(2 * time.Second)
						sessionInfo, err = session.ResolveSessionInfo(spec)
					}
					if err != nil {
						return fmt.Errorf("could not find session for '%s' after multiple retries: %w", spec, err)
					}
				}
			}

			toolFormatters := map[string]formatters.ToolFormatter{
				"Write":     formatters.MakeWriteFormatter(0),
				"Edit":      formatters.MakeWriteFormatter(0),
				"Read":      formatters.FormatReadTool,
				"TodoWrite": formatters.FormatTodoWriteTool,
			}

			// If resolved session has no LogFilePath (common for daemon-resolved agent jobs),
			// try to enrich it from the scanner which can find JSONL transcript files.
			if sessionInfo.LogFilePath == "" {
				ulogStream.Debug("Session resolved without LogFilePath, scanning for transcript file").
					Field("session_id", sessionInfo.SessionID).
					Emit()

				scanner := session.NewScannerWithoutDaemon()
				allSessions, scanErr := scanner.Scan()
				if scanErr == nil {
					for _, s := range allSessions {
						if s.SessionID == sessionInfo.SessionID && s.LogFilePath != "" {
							sessionInfo.LogFilePath = s.LogFilePath
							break
						}
						// Also try matching by job info
						for _, job := range s.Jobs {
							for _, sJob := range sessionInfo.Jobs {
								if job.Plan == sJob.Plan && job.Job == sJob.Job && s.LogFilePath != "" {
									sessionInfo.LogFilePath = s.LogFilePath
								}
							}
						}
						if sessionInfo.LogFilePath != "" {
							break
						}
					}
				}

				if sessionInfo.LogFilePath != "" {
					ulogStream.Debug("Found transcript file via scanner").
						Field("log_file_path", sessionInfo.LogFilePath).
						Emit()
				}
			}

			// Route to appropriate source
			daemonClient := daemon.New()
			defer daemonClient.Close()

			src := provider.SelectSource(sessionInfo, daemonClient)

			ulogStream.Debug("Streaming logs").
				Field("session_id", sessionInfo.SessionID).
				Field("provider", sessionInfo.Provider).
				Field("log_file_path", sessionInfo.LogFilePath).
				Emit()

			ch, err := src.Stream(cmd.Context(), sessionInfo)
			if err != nil {
				return fmt.Errorf("failed to stream transcript: %w", err)
			}

			jsonEncoder := json.NewEncoder(os.Stdout)

			for entry := range ch {
				if jsonOutput {
					jsonEncoder.Encode(entry)
				} else {
					display.DisplayUnifiedEntry(entry, "full", toolFormatters)
				}
			}

			return nil
		},
	}
	return cmd
}
