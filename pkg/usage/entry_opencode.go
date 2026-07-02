package usage

import (
	"path/filepath"
	"strings"

	"github.com/grovetools/agentlogs/internal/opencode"
)

// opencodeFileTokenStats computes token stats for an opencode session.
//
// OpenCode has no single transcript file: the scanner (and the plugin's
// metadata pointer) hand us the session *info* file at
// <storage>/session/<projectID>/<ses_...>.json, from which both the storage
// root and the native session id are derived, and the per-message token
// usage is read through the fragment assembler (message/<sessionID>/msg_*).
// Previously this path was fed to the Claude JSONL parser, which silently
// returned zeros.
//
// Totals sum every usage-bearing message; the latest figures come from the
// last such message. OpenCode's per-message input excludes cache, so the
// latest context size is input + cache_read + cache_write.
func opencodeFileTokenStats(path string) (FileStats, error) {
	sessionID := strings.TrimSuffix(filepath.Base(path), ".json")
	if !strings.HasPrefix(sessionID, "ses") {
		// Not a session info file (e.g. an archived flat transcript);
		// fall back to the historical Claude-shaped parsing.
		return FileTokenStats(path)
	}
	// <storage>/session/<projectID>/<ses_...>.json -> <storage>
	storageDir := filepath.Dir(filepath.Dir(filepath.Dir(path)))

	assembler, err := opencode.NewAssemblerWithDir(storageDir)
	if err != nil {
		return FileStats{}, err
	}
	entries, err := assembler.AssembleTranscript(sessionID)
	if err != nil {
		return FileStats{}, err
	}

	var stats FileStats
	var last *opencode.TokenUsage
	for i := range entries {
		tokens := entries[i].Tokens
		if tokens == nil {
			continue
		}
		stats.MessageCount++
		stats.TotalInputTokens += tokens.Input
		stats.TotalOutputTokens += tokens.Output
		stats.TotalCacheRead += tokens.CacheRead
		stats.TotalCacheCreation += tokens.CacheWrite
		last = tokens
	}
	if last != nil {
		stats.LatestOutputTokens = last.Output
		stats.LatestCacheReadTokens = last.CacheRead
		stats.LatestContextSize = last.Input + last.CacheRead + last.CacheWrite
	}
	return stats, nil
}
