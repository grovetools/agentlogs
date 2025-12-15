package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mattsolo1/grove-agent-logs/internal/display"
	"github.com/mattsolo1/grove-agent-logs/internal/formatters"
	"github.com/mattsolo1/grove-agent-logs/internal/session"
	"github.com/spf13/cobra"
)

func newStreamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "stream <plan/job>",
		Short:  "Stream logs for a specific plan/job execution",
		Long:   "Finds and tails the agent transcript log for a given plan/job, formatting the output as it streams.",
		Args:   cobra.ExactArgs(1),
		Hidden: true, // Internal command for now
		RunE: func(cmd *cobra.Command, args []string) error {
			jobSpec := args[0]

			parts := strings.Split(jobSpec, "/")
			if len(parts) != 2 {
				return fmt.Errorf("invalid job specification: expected format 'plan/job.md'")
			}
			planName := parts[0]
			jobName := parts[1]

			scanner := session.NewScanner()
			// This initial scan helps locate the session.
			allSessions, err := scanner.Scan()
			if err != nil {
				return err
			}

			var match *session.SessionInfo
			for i, s := range allSessions {
				for _, job := range s.Jobs {
					if job.Plan == planName && job.Job == jobName {
						match = &allSessions[i]
						break
					}
				}
				if match != nil {
					break
				}
			}

			if match == nil {
				// Wait and retry multiple times in case the session hasn't been registered yet.
				// This can happen when the job just started and the agent is launching.
				maxRetries := 5
				for attempt := 0; attempt < maxRetries && match == nil; attempt++ {
					time.Sleep(2 * time.Second)
					allSessions, _ = scanner.Scan()
					for i, s := range allSessions {
						for _, job := range s.Jobs {
							if job.Plan == planName && job.Job == jobName {
								match = &allSessions[i]
								break
							}
						}
						if match != nil {
							break
						}
					}
				}
				if match == nil {
					return fmt.Errorf("could not find session for job %s after %d retries", jobSpec, maxRetries)
				}
			}

			// Tail the log file from the end.
			return tailLogFile(match)
		},
	}
	return cmd
}

func tailLogFile(s *session.SessionInfo) error {
	file, err := os.Open(s.LogFilePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Seek to the end of the file to start tailing.
	file.Seek(0, io.SeekEnd)
	reader := bufio.NewReader(file)

	toolFormatters := map[string]formatters.ToolFormatter{
		"Write":     formatters.MakeWriteFormatter(0),
		"Edit":      formatters.MakeWriteFormatter(0),
		"Read":      formatters.FormatReadTool,
		"TodoWrite": formatters.FormatTodoWriteTool,
	}

	lineCount := 0
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			// Check if file has been closed or removed
			if _, statErr := os.Stat(s.LogFilePath); statErr != nil {
				fmt.Fprintf(os.Stderr, "Log file no longer accessible: %v\n", statErr)
				return statErr
			}
			time.Sleep(500 * time.Millisecond) // Wait for new content
			continue
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading log file after %d lines: %v\n", lineCount, err)
			return err
		}

		lineCount++
		if len(line) > 0 {
			if strings.Contains(s.LogFilePath, "/.codex/") {
				display.DisplayCodexLogLine(line)
			} else {
				var entry display.TranscriptEntry
				if err := json.Unmarshal(line, &entry); err == nil {
					// Use "full" detail level for streaming to see all tool details.
					display.DisplayTranscriptEntry(entry, "full", toolFormatters)
				}
			}
		}
	}
}
