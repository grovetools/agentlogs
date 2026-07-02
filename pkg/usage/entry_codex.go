package usage

import (
	"bufio"
	"os"
	"strings"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

// FileTokenStatsForProvider routes per-file token accounting by provider.
// Codex rollout files carry usage on end-of-turn token_count events instead
// of Claude's message.usage shape; opencode has no transcript file at all —
// its path is a session info file whose tokens are read through the fragment
// assembler. Every other provider keeps the historical Claude-shaped parsing.
func FileTokenStatsForProvider(path, provider string) (FileStats, error) {
	switch provider {
	case "codex":
		return codexFileTokenStats(path)
	case "opencode":
		return opencodeFileTokenStats(path)
	default:
		return FileTokenStats(path)
	}
}

// codexFileTokenStats reads a codex rollout JSONL transcript and returns
// cumulative token totals plus the latest turn's context figures. Codex's
// token_count events already carry a running total (total_token_usage), so
// the totals come from the final usage-bearing event rather than summing;
// the latest figures come from that event's last_token_usage. MessageCount
// counts usage-bearing turns. Malformed lines are skipped; only an open
// error returns.
func codexFileTokenStats(path string) (FileStats, error) {
	f, err := os.Open(path)
	if err != nil {
		return FileStats{}, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxLineSize)

	var stats FileStats
	var last transcript.CodexTokenCount
	var haveUsage bool
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		// Cheap prefilter: only token_count events matter.
		if !strings.Contains(string(line), "token_count") {
			continue
		}
		tc, ok := transcript.ParseCodexTokenCountLine(line)
		if !ok {
			continue
		}
		stats.MessageCount++
		last = tc
		haveUsage = true
	}
	if haveUsage {
		stats.TotalInputTokens = last.Total.Input
		stats.TotalOutputTokens = last.Total.Output
		stats.TotalCacheRead = last.Total.CacheRead
		stats.TotalCacheCreation = last.Total.CacheWrite // codex reports no cache-write class; stays 0
		stats.LatestOutputTokens = last.Last.Output
		stats.LatestCacheReadTokens = last.Last.CacheRead
		// input_tokens is the full prompt (cached + fresh) — the codex
		// analogue of Claude's cache_read+cache_creation+input context size.
		stats.LatestContextSize = last.Last.Input + last.Last.CacheRead
	}
	if err := scanner.Err(); err != nil {
		return stats, err
	}
	return stats, nil
}
