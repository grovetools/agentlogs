package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/grovetools/core/cli"
	"github.com/spf13/cobra"

	"github.com/grovetools/agentlogs/internal/session"
	"github.com/grovetools/agentlogs/pkg/usage"
)

// TokenStats aggregates token statistics for a session
type TokenStats struct {
	SessionID             string `json:"session_id"`
	Provider              string `json:"provider"`
	MessageCount          int    `json:"message_count"`
	TotalInputTokens      int    `json:"total_input_tokens"`
	TotalOutputTokens     int    `json:"total_output_tokens"`
	TotalCacheCreation    int    `json:"total_cache_creation_tokens"`
	TotalCacheRead        int    `json:"total_cache_read_tokens"`
	LatestContextSize     int    `json:"latest_context_size"`
	LatestCacheReadTokens int    `json:"latest_cache_read_tokens"`
	LatestOutputTokens    int    `json:"latest_output_tokens"`
}

func newTokensCmd() *cobra.Command {
	var jsonOutput bool

	cmd := cli.NewStandardCommand("tokens", "Show token usage statistics for a session")
	cmd.Use = "tokens <spec>"
	cmd.Long = `Shows token usage statistics for a session transcript.

<spec> can be a plan/job, a session ID, or a direct path to a log file.

The command extracts token counts from the API usage data in the transcript,
showing both cumulative totals and the latest context window size.`
	cmd.Args = cobra.ExactArgs(1)

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		spec := args[0]

		var sessionInfo *session.SessionInfo
		var err error

		// Fast path: if spec is a file path, read it directly
		if fileInfo, statErr := os.Stat(spec); statErr == nil && !fileInfo.IsDir() {
			provider := "claude"
			if strings.Contains(spec, "/.codex/") {
				provider = "codex"
			} else if strings.Contains(spec, "/opencode/storage/") {
				// An opencode session info file
				// (<storage>/session/<projectID>/<ses_*>.json); tokens are
				// read through the fragment assembler.
				provider = "opencode"
			}

			sessionID := "unknown"
			if provider == "opencode" {
				sessionID = strings.TrimSuffix(filepath.Base(spec), ".json")
			}
			pathParts := strings.Split(spec, "/")
			for i, part := range pathParts {
				if part == ".claude" || part == ".codex" {
					if i+1 < len(pathParts) {
						sessionID = pathParts[i+1]
					}
					break
				}
			}

			sessionInfo = &session.SessionInfo{
				LogFilePath: spec,
				Provider:    provider,
				SessionID:   sessionID,
			}
		} else {
			sessionInfo, err = session.ResolveSessionInfo(spec)
			if err != nil {
				return fmt.Errorf("could not resolve session for '%s': %w", spec, err)
			}
		}

		fileStats, err := usage.FileTokenStatsForProvider(sessionInfo.LogFilePath, sessionInfo.Provider)
		if err != nil {
			return fmt.Errorf("error reading log file: %w", err)
		}

		stats := TokenStats{
			SessionID:             sessionInfo.SessionID,
			Provider:              sessionInfo.Provider,
			MessageCount:          fileStats.MessageCount,
			TotalInputTokens:      fileStats.TotalInputTokens,
			TotalOutputTokens:     fileStats.TotalOutputTokens,
			TotalCacheCreation:    fileStats.TotalCacheCreation,
			TotalCacheRead:        fileStats.TotalCacheRead,
			LatestContextSize:     fileStats.LatestContextSize,
			LatestCacheReadTokens: fileStats.LatestCacheReadTokens,
			LatestOutputTokens:    fileStats.LatestOutputTokens,
		}

		// Output results
		if jsonOutput {
			jsonData, err := json.MarshalIndent(stats, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal stats: %w", err)
			}
			fmt.Println(string(jsonData))
		} else {
			fmt.Printf("Token Usage for Session: %s\n", stats.SessionID)
			fmt.Printf("Provider: %s\n", stats.Provider)
			fmt.Println(strings.Repeat("─", 50))
			fmt.Printf("Messages processed:      %d\n", stats.MessageCount)
			fmt.Println()
			fmt.Println("Cumulative Totals:")
			fmt.Printf("  Input tokens:          %d\n", stats.TotalInputTokens)
			fmt.Printf("  Output tokens:         %d\n", stats.TotalOutputTokens)
			fmt.Printf("  Cache creation:        %d\n", stats.TotalCacheCreation)
			fmt.Printf("  Cache read:            %d\n", stats.TotalCacheRead)
			fmt.Println()
			fmt.Println("Latest Message:")
			fmt.Printf("  Context size:          %d tokens\n", stats.LatestContextSize)
			fmt.Printf("  Cache read:            %d tokens\n", stats.LatestCacheReadTokens)
			fmt.Printf("  Output:                %d tokens\n", stats.LatestOutputTokens)
		}

		return nil
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")

	return cmd
}
