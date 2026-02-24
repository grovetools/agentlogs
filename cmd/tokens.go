package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/grovetools/agentlogs/internal/session"
	"github.com/grovetools/core/cli"
	"github.com/spf13/cobra"
)

// TokenUsage represents the usage stats from a Claude API response
type TokenUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// TokenStats aggregates token statistics for a session
type TokenStats struct {
	SessionID               string `json:"session_id"`
	Provider                string `json:"provider"`
	MessageCount            int    `json:"message_count"`
	TotalInputTokens        int    `json:"total_input_tokens"`
	TotalOutputTokens       int    `json:"total_output_tokens"`
	TotalCacheCreation      int    `json:"total_cache_creation_tokens"`
	TotalCacheRead          int    `json:"total_cache_read_tokens"`
	LatestContextSize       int    `json:"latest_context_size"`
	LatestCacheReadTokens   int    `json:"latest_cache_read_tokens"`
	LatestOutputTokens      int    `json:"latest_output_tokens"`
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
			}

			sessionID := "unknown"
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

		// Read and parse the log file
		file, err := os.Open(sessionInfo.LogFilePath)
		if err != nil {
			return err
		}
		defer file.Close()

		stats := TokenStats{
			SessionID: sessionInfo.SessionID,
			Provider:  sessionInfo.Provider,
		}

		scanner := bufio.NewScanner(file)
		const maxScanTokenSize = 1024 * 1024
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, maxScanTokenSize)

		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			// Parse the JSON line to extract usage
			var entry map[string]interface{}
			if err := json.Unmarshal(line, &entry); err != nil {
				continue
			}

			// Look for message.usage
			message, ok := entry["message"].(map[string]interface{})
			if !ok {
				continue
			}

			usage, ok := message["usage"].(map[string]interface{})
			if !ok {
				continue
			}

			stats.MessageCount++

			// Extract token counts
			if v, ok := usage["input_tokens"].(float64); ok {
				stats.TotalInputTokens += int(v)
			}
			if v, ok := usage["output_tokens"].(float64); ok {
				stats.TotalOutputTokens += int(v)
				stats.LatestOutputTokens = int(v)
			}
			if v, ok := usage["cache_creation_input_tokens"].(float64); ok {
				stats.TotalCacheCreation += int(v)
			}
			if v, ok := usage["cache_read_input_tokens"].(float64); ok {
				stats.TotalCacheRead += int(v)
				stats.LatestCacheReadTokens = int(v)
			}

			// Calculate latest context size (cache_read + cache_creation + input)
			cacheRead := 0
			cacheCreation := 0
			input := 0
			if v, ok := usage["cache_read_input_tokens"].(float64); ok {
				cacheRead = int(v)
			}
			if v, ok := usage["cache_creation_input_tokens"].(float64); ok {
				cacheCreation = int(v)
			}
			if v, ok := usage["input_tokens"].(float64); ok {
				input = int(v)
			}
			stats.LatestContextSize = cacheRead + cacheCreation + input
		}

		if err := scanner.Err(); err != nil {
			return fmt.Errorf("error reading log file: %w", err)
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
