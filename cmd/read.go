package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	aglogs_config "github.com/mattsolo1/grove-agent-logs/config"
	"github.com/mattsolo1/grove-agent-logs/internal/display"
	"github.com/mattsolo1/grove-agent-logs/internal/formatters"
	"github.com/mattsolo1/grove-agent-logs/internal/session"
	core_config "github.com/mattsolo1/grove-core/config"
	"github.com/spf13/cobra"
)

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

			// Fast path: if spec is a file path, read it directly
			if fileInfo, statErr := os.Stat(spec); statErr == nil && !fileInfo.IsDir() {
				// Construct minimal SessionInfo from the file path
				provider := "claude"
				if strings.Contains(spec, "/.codex/") {
					provider = "codex"
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
					Provider:    provider,
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
			var targetJob *session.JobInfo
			var nextJobLine int = -1
			parts := strings.Split(spec, "/")
			if len(parts) == 2 {
				planName := parts[0]
				jobName := parts[1]
				for i, job := range sessionInfo.Jobs {
					if job.Plan == planName && job.Job == jobName {
						targetJob = &sessionInfo.Jobs[i]
						if i+1 < len(sessionInfo.Jobs) {
							nextJobLine = sessionInfo.Jobs[i+1].LineIndex
						}
						break
					}
				}
			}

			startLine := 0
			if targetJob != nil {
				startLine = targetJob.LineIndex
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

			// --- Log Reading ---
			file, err := os.Open(sessionInfo.LogFilePath)
			if err != nil {
				return err
			}
			defer file.Close()

			fileScanner := bufio.NewScanner(file)
			const maxScanTokenSize = 1024 * 1024 // 1MB
			buf := make([]byte, 0, 64*1024)
			fileScanner.Buffer(buf, maxScanTokenSize)

			var logContentBuilder strings.Builder
			lineIndex := 0
			inRange := false
			for fileScanner.Scan() {
				if lineIndex >= startLine {
					inRange = true
				}
				if nextJobLine != -1 && lineIndex >= nextJobLine {
					break
				}
				if inRange {
					line := fileScanner.Bytes()
					if len(line) > 0 {
						logContentBuilder.Write(line)
						logContentBuilder.WriteString("\n")
					}
				}
				lineIndex++
			}
			logContent := logContentBuilder.String()

			// --- Output ---
			if jsonOutput {
				provider := "claude"
				if strings.Contains(sessionInfo.LogFilePath, "/.codex/") {
					provider = "codex"
				}
				output := struct {
					LogContent  string `json:"log_content"`
					LogFilePath string `json:"log_file_path"`
					Provider    string `json:"provider"`
					SessionID   string `json:"session_id"`
				}{
					LogContent:  logContent,
					LogFilePath: sessionInfo.LogFilePath,
					Provider:    provider,
					SessionID:   sessionInfo.SessionID,
				}
				jsonData, err := json.Marshal(output)
				if err != nil {
					return fmt.Errorf("failed to marshal to JSON: %w", err)
				}
				fmt.Println(string(jsonData))
			} else {
				// Human-readable output

				// Create a new scanner to process the captured content
				contentScanner := bufio.NewScanner(strings.NewReader(logContent))
				// Increase the buffer to handle long lines, matching the fileScanner config.
				const maxScanTokenSize = 1024 * 1024 // 1MB
				buf := make([]byte, 0, 64*1024)
				contentScanner.Buffer(buf, maxScanTokenSize)
				for contentScanner.Scan() {
					line := contentScanner.Bytes()
					if len(line) > 0 {
						if strings.Contains(sessionInfo.LogFilePath, "/.codex/") {
							display.DisplayCodexLogLine(line)
						} else {
							var entry display.TranscriptEntry
							if err := json.Unmarshal(line, &entry); err == nil {
								display.DisplayTranscriptEntry(entry, detailLevel, toolFormatters)
							}
						}
					}
				}
			}

			return nil
		},
	}

	cmd.Flags().String("detail", "", "Set detail level for output ('summary' or 'full'). Overrides config.")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format with additional metadata")
	return cmd
}
