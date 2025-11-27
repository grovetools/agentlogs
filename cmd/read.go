package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	aglogs_config "github.com/mattsolo1/grove-agent-logs/config"
	"github.com/mattsolo1/grove-agent-logs/internal/display"
	"github.com/mattsolo1/grove-agent-logs/internal/formatters"
	"github.com/mattsolo1/grove-agent-logs/internal/session"
	core_config "github.com/mattsolo1/grove-core/config"
	"github.com/spf13/cobra"
)

func newReadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "read <plan/job>",
		Short: "Read logs for a specific plan/job execution",
		Long:  "Read logs starting from a specific plan/job execution until the next job or end of session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobSpec := args[0]

			parts := strings.Split(jobSpec, "/")
			if len(parts) != 2 {
				return fmt.Errorf("invalid job specification: expected format 'plan/job.md'")
			}
			planName := parts[0]
			jobName := parts[1]

			sessionID, _ := cmd.Flags().GetString("session")
			projectFilter, _ := cmd.Flags().GetString("project")
			detailFlag, _ := cmd.Flags().GetString("detail")

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

			scanner := session.NewScanner()
			allSessions, err := scanner.Scan()
			if err != nil {
				return err
			}

			type jobMatch struct {
				sessionInfo session.SessionInfo
				job         session.JobInfo
				nextJobLine int
			}
			var found []jobMatch

			// Group sessions by SessionID to handle multiple log files per session
			sessionsByID := make(map[string][]session.SessionInfo)
			for _, s := range allSessions {
				if projectFilter != "" && !strings.Contains(strings.ToLower(s.ProjectName), strings.ToLower(projectFilter)) {
					continue
				}
				if sessionID != "" && s.SessionID != sessionID {
					continue
				}
				sessionsByID[s.SessionID] = append(sessionsByID[s.SessionID], s)
			}

			for _, sessionLogs := range sessionsByID {
				// Sort logs chronologically within a session
				sort.Slice(sessionLogs, func(i, j int) bool {
					return sessionLogs[i].StartedAt.Before(sessionLogs[j].StartedAt)
				})

				var allJobsInSession []session.JobInfo
				for _, s := range sessionLogs {
					allJobsInSession = append(allJobsInSession, s.Jobs...)
				}

				for i, job := range allJobsInSession {
					if job.Plan == planName && job.Job == jobName {
						nextLine := -1
						if i+1 < len(allJobsInSession) {
							nextLine = allJobsInSession[i+1].LineIndex
						}
						// Find which log file this job belongs to
						for _, s := range sessionLogs {
							for _, sJob := range s.Jobs {
								if sJob == job {
									found = append(found, jobMatch{
										sessionInfo: s,
										job:         job,
										nextJobLine: nextLine,
									})
									goto nextSession
								}
							}
						}
					}
				}
			nextSession:
			}

			if len(found) == 0 {
				return fmt.Errorf("no sessions found with job %s", jobSpec)
			}

			sessionGroups := make(map[string][]jobMatch)
			for _, match := range found {
				sessionGroups[match.sessionInfo.SessionID] = append(sessionGroups[match.sessionInfo.SessionID], match)
			}

			if len(sessionGroups) > 1 && sessionID == "" {
				fmt.Printf("Multiple sessions found with job %s:\n\n", jobSpec)
				for sid, matches := range sessionGroups {
					fmt.Printf("  Project: %s\n", matches[0].sessionInfo.ProjectName)
					fmt.Printf("  Session: %s\n\n", sid)
				}
				fmt.Println("Please specify a session with --session or filter by project with --project")
				return nil
			}

			var matchesToUse []jobMatch
			if len(sessionGroups) == 1 {
				for _, v := range sessionGroups {
					matchesToUse = v
				}
			} else {
				matchesToUse = sessionGroups[sessionID]
			}

			if len(matchesToUse) == 0 {
				return fmt.Errorf("no matching session found")
			}

			match := matchesToUse[0]

			fmt.Printf("=== Job: %s/%s ===\n", match.job.Plan, match.job.Job)
			fmt.Printf("Project: %s\n", match.sessionInfo.ProjectName)
			fmt.Printf("Session: %s\n", match.sessionInfo.SessionID)
			fmt.Printf("Starting at line: %d\n\n", match.job.LineIndex)

			file, err := os.Open(match.sessionInfo.LogFilePath)
			if err != nil {
				return err
			}
			defer file.Close()

			fileScanner := bufio.NewScanner(file)
			const maxScanTokenSize = 1024 * 1024 // 1MB
			buf := make([]byte, 0, 64*1024)
			fileScanner.Buffer(buf, maxScanTokenSize)

			lineIndex := 0
			inRange := false
			for fileScanner.Scan() {
				if lineIndex >= match.job.LineIndex {
					inRange = true
				}
				if match.nextJobLine != -1 && lineIndex >= match.nextJobLine {
					break
				}
				if inRange {
					line := fileScanner.Bytes()
					if len(line) > 0 {
						if strings.Contains(match.sessionInfo.LogFilePath, "/.codex/") {
							display.DisplayCodexLogLine(line)
						} else {
							var entry display.TranscriptEntry
							if err := json.Unmarshal(line, &entry); err == nil {
								display.DisplayTranscriptEntry(entry, detailLevel, toolFormatters)
							}
						}
					}
				}
				lineIndex++
			}

			if match.nextJobLine != -1 {
				fmt.Printf("\n=== Next job starts at line %d ===\n", match.nextJobLine)
			} else {
				fmt.Println("\n=== End of session ===")
			}

			return nil
		},
	}

	cmd.Flags().StringP("session", "s", "", "Specify session ID (required if multiple matches)")
	cmd.Flags().StringP("project", "p", "", "Filter by project name")
	cmd.Flags().String("detail", "", "Set detail level for output ('summary' or 'full'). Overrides config.")

	return cmd
}
