package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/grovetools/agentlogs/internal/display"
	"github.com/grovetools/agentlogs/internal/formatters"
	"github.com/grovetools/agentlogs/internal/opencode"
	"github.com/grovetools/agentlogs/internal/session"
	"github.com/grovetools/agentlogs/internal/transcript"
	grovelogging "github.com/grovetools/core/logging"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/spf13/cobra"
)

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

			// Try daemon SSE streaming first — this is the primary path in the new architecture.
			// If the daemon is managing this job, it provides centralized log streaming.
			daemonClient := daemon.New()
			defer daemonClient.Close()
			if daemonClient.IsRunning() {
				if job, _ := daemonClient.GetJob(context.Background(), sessionInfo.SessionID); job != nil {
					return streamFromDaemon(daemonClient, sessionInfo)
				}
			}

			// Handle OpenCode sessions specially
			if sessionInfo.Provider == "opencode" {
				return streamOpenCodeSession(sessionInfo)
			}

			// Tail the log file from the end (fallback when daemon is unavailable).
			return tailLogFile(sessionInfo)
		},
	}
	return cmd
}

// streamOpenCodeSession watches an OpenCode session for new messages and displays them.
func streamOpenCodeSession(s *session.SessionInfo) error {
	assembler, err := opencode.NewAssembler()
	if err != nil {
		return fmt.Errorf("creating OpenCode assembler: %w", err)
	}

	toolFormatters := map[string]formatters.ToolFormatter{
		"Write":     formatters.MakeWriteFormatter(0),
		"Edit":      formatters.MakeWriteFormatter(0),
		"Read":      formatters.FormatReadTool,
		"TodoWrite": formatters.FormatTodoWriteTool,
	}

	normalizer := transcript.NewOpenCodeNormalizer()

	// Track which messages we've already displayed
	seenMessages := make(map[string]bool)

	// Initial display of existing messages
	entries, err := assembler.AssembleTranscript(s.SessionID)
	if err != nil {
		return fmt.Errorf("assembling OpenCode transcript: %w", err)
	}

	for _, entry := range entries {
		seenMessages[entry.MessageID] = true
		unified := normalizer.NormalizeEntry(entry)
		if unified != nil {
			display.DisplayUnifiedEntry(*unified, "full", toolFormatters)
		}
	}

	ulogStream.Info("Watching for new messages").
		Field("session_id", s.SessionID).
		Pretty("\n--- Watching for new messages... ---").
		PrettyOnly().
		Emit()

	// Poll for new messages
	for {
		time.Sleep(1 * time.Second)

		entries, err := assembler.AssembleTranscript(s.SessionID)
		if err != nil {
			continue // Ignore transient errors
		}

		for _, entry := range entries {
			if !seenMessages[entry.MessageID] {
				seenMessages[entry.MessageID] = true
				unified := normalizer.NormalizeEntry(entry)
				if unified != nil {
					display.DisplayUnifiedEntry(*unified, "full", toolFormatters)
				}
			}
		}
	}
}

// streamFromDaemon subscribes to the daemon's SSE log stream for a job.
// This is the primary streaming path when the daemon is managing the job.
func streamFromDaemon(client daemon.Client, s *session.SessionInfo) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := client.StreamJobLogs(ctx, s.SessionID)
	if err != nil {
		return fmt.Errorf("failed to subscribe to daemon log stream: %w", err)
	}

	toolFormatters := map[string]formatters.ToolFormatter{
		"Write":     formatters.MakeWriteFormatter(0),
		"Edit":      formatters.MakeWriteFormatter(0),
		"Read":      formatters.FormatReadTool,
		"TodoWrite": formatters.FormatTodoWriteTool,
	}

	var normalizer transcript.Normalizer
	if s.Provider == "codex" {
		normalizer = transcript.NewCodexNormalizer()
	} else {
		normalizer = transcript.NewClaudeNormalizer()
	}

	ulogStream.Info("Streaming from daemon").
		Field("job_id", s.SessionID).
		Pretty(fmt.Sprintf("\n--- Tailing logs for job %s via daemon... ---\n", s.SessionID)).
		PrettyOnly().
		Emit()

	for event := range ch {
		if event.Event == "log" && event.Line != nil {
			lineBytes := []byte(event.Line.Line)
			entry, normErr := normalizer.NormalizeLine(lineBytes)

			if normErr != nil {
				// Not valid JSON; it's a standard text log (e.g., from a bash job)
				fmt.Println(event.Line.Line)
			} else if entry != nil {
				// Valid agent JSON log entry
				display.DisplayUnifiedEntry(*entry, "full", toolFormatters)
			}
			// If normErr == nil && entry == nil, the line was intentionally skipped by the normalizer

		} else if event.Event == "status" {
			if event.Status == "completed" || event.Status == "failed" || event.Status == "cancelled" {
				ulogStream.Info("Job finished").
					Field("status", event.Status).
					Field("error", event.Error).
					Pretty(fmt.Sprintf("\n--- Job finished (status: %s) ---\n", event.Status)).
					PrettyOnly().
					Emit()

				if event.Error != "" {
					fmt.Fprintf(os.Stderr, "Error: %s\n", event.Error)
				}
				return nil
			}
		}
	}
	return nil
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

	// Select appropriate normalizer based on provider
	var normalizer transcript.Normalizer
	if strings.Contains(s.LogFilePath, "/.codex/") {
		normalizer = transcript.NewCodexNormalizer()
	} else {
		normalizer = transcript.NewClaudeNormalizer()
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
			if entry, normErr := normalizer.NormalizeLine(line); normErr == nil && entry != nil {
				display.DisplayUnifiedEntry(*entry, "full", toolFormatters)
			}
		}
	}
}
