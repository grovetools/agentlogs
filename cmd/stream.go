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
		Use:    "stream <spec>",
		Short:  "Stream logs for a specific job, session, or log file",
		Long:   "Finds and tails the agent transcript log. <spec> can be a plan/job, a session ID, or a direct path to a log file.",
		Args:   cobra.ExactArgs(1),
		Hidden: true, // Internal command for now
		RunE: func(cmd *cobra.Command, args []string) error {
			spec := args[0]

			// Fast path: if spec is a file path, stream it directly
			if _, err := os.Stat(spec); err == nil {
				provider := "claude"
				if strings.Contains(spec, "/.codex/") {
					provider = "codex"
				}
				sessionInfo := &session.SessionInfo{
					LogFilePath: spec,
					Ecosystem: provider, // A bit of a hack, but good enough
				}
				return tailLogFile(sessionInfo)
			}

			// Slow path: resolve session from spec
			sessionInfo, err := session.ResolveSessionInfo(spec)
			if err != nil {
				// Retry logic for newly started jobs
				maxRetries := 5
				for attempt := 0; attempt < maxRetries && err != nil; attempt++ {
					time.Sleep(2 * time.Second)
					sessionInfo, err = session.ResolveSessionInfo(spec)
				}
				if err != nil {
					return fmt.Errorf("could not find session for '%s' after multiple retries: %w", spec, err)
				}
			}

			// Tail the log file from the end.
			return tailLogFile(sessionInfo)
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
