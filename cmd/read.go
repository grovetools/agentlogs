package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	aglogs_config "github.com/grovetools/agentlogs/config"
	"github.com/grovetools/agentlogs/internal/display"
	"github.com/grovetools/agentlogs/internal/formatters"
	"github.com/grovetools/agentlogs/internal/provider"
	"github.com/grovetools/agentlogs/internal/session"
	"github.com/grovetools/agentlogs/internal/transcript"
	core_config "github.com/grovetools/core/config"
	grovelogging "github.com/grovetools/core/logging"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/spf13/cobra"
)

var ulogRead = grovelogging.NewUnifiedLogger("grove-agent-logs.cmd.read")

func newReadCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "read <spec>",
		Short: "Read logs for a specific job, session, or log file",
		Long:  "Reads logs for a job execution. <spec> can be a plan/job, a session ID, or a direct path to a job or log file.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			spec := args[0]
			detailFlag, _ := cmd.Flags().GetString("detail")
			jsonOutput, _ := cmd.Flags().GetBool("json")

			var sessionInfo *session.SessionInfo
			var err error

			// Fast path: if spec is an actual log file path (not a plan/job spec),
			// read it directly. Uses isLogFilePath to avoid matching plan markdown
			// files that happen to exist in the cwd.
			if isLogFilePath(spec) {
				// Construct minimal SessionInfo from the file path
				prov := "claude"
				if strings.Contains(spec, "/.codex/") {
					prov = "codex"
				}

				// Extract session ID and project name from path if possible
				sessionID := "unknown"
				projectName := "unknown"
				pathParts := strings.Split(spec, "/")
				for i, part := range pathParts {
					if part == ".claude" || part == ".codex" {
						if i+1 < len(pathParts) {
							sessionID = pathParts[i+1]
						}
						if i > 0 {
							projectName = pathParts[i-1]
						}
						break
					}
				}

				sessionInfo = &session.SessionInfo{
					LogFilePath: spec,
					Provider:    prov,
					SessionID:   sessionID,
					ProjectName: projectName,
					Jobs:        []session.JobInfo{},
				}
			} else {
				// Slow path: resolve session from spec
				sessionInfo, err = session.ResolveSessionInfo(spec)
				if err != nil {
					return fmt.Errorf("could not resolve session for '%s': %w", spec, err)
				}
			}

			// Find the specific job within the session if the spec was a plan/job
			startLine := 0
			endLine := -1 // -1 = read to end
			parts := strings.Split(spec, "/")
			if len(parts) == 2 {
				planName := parts[0]
				jobName := parts[1]
				for i, job := range sessionInfo.Jobs {
					if job.Plan == planName && job.Job == jobName {
						startLine = job.LineIndex
						if i+1 < len(sessionInfo.Jobs) {
							endLine = sessionInfo.Jobs[i+1].LineIndex
						}
						break
					}
				}
			}

			// --- Configuration Loading ---
			var detailLevel string
			var maxDiffLines int
			coreCfg, err := core_config.LoadDefault()
			if err == nil {
				var aglogsCfg aglogs_config.Config
				if err := coreCfg.UnmarshalExtension("aglogs", &aglogsCfg); err == nil {
					detailLevel = aglogsCfg.Transcript.DetailLevel
					maxDiffLines = aglogsCfg.Transcript.MaxDiffLines
				}
			}
			if detailFlag != "" {
				detailLevel = detailFlag
			} else if detailLevel == "" {
				detailLevel = "summary"
			}
			toolFormatters := map[string]formatters.ToolFormatter{
				"Write":     formatters.MakeWriteFormatter(maxDiffLines),
				"Edit":      formatters.MakeWriteFormatter(maxDiffLines),
				"Read":      formatters.FormatReadTool,
				"TodoWrite": formatters.FormatTodoWriteTool,
			}

			// --- Read via provider ---
			daemonClient := daemon.New()
			defer daemonClient.Close()

			src := provider.SelectSource(sessionInfo, daemonClient)
			opts := provider.ReadOptions{
				DetailLevel:  detailLevel,
				MaxDiffLines: maxDiffLines,
				StartLine:    startLine,
				EndLine:      endLine,
			}

			entries, err := src.Read(cmd.Context(), sessionInfo, opts)
			if err != nil {
				return fmt.Errorf("failed to read transcript: %w", err)
			}

			// --- Output ---
			if jsonOutput {
				output := struct {
					Entries     []transcript.UnifiedEntry `json:"entries"`
					LogFilePath string                    `json:"log_file_path"`
					Provider    string                    `json:"provider"`
					SessionID   string                    `json:"session_id"`
				}{
					Entries:     entries,
					LogFilePath: sessionInfo.LogFilePath,
					Provider:    sessionInfo.Provider,
					SessionID:   sessionInfo.SessionID,
				}
				jsonData, err := json.Marshal(output)
				if err != nil {
					return fmt.Errorf("failed to marshal to JSON: %w", err)
				}
				ulogRead.Info("Read log content").
					Field("session_id", sessionInfo.SessionID).
					Field("provider", sessionInfo.Provider).
					Field("entry_count", len(entries)).
					Pretty(string(jsonData)).
					PrettyOnly().
					Emit()
			} else {
				display.DisplayUnifiedTranscript(entries, detailLevel, toolFormatters)
			}

			return nil
		},
	}

	cmd.Flags().String("detail", "", "Set detail level for output ('summary' or 'full'). Overrides config.")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format with additional metadata")
	return cmd
}
