package usage

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

// maxLineSize bounds a single JSONL line. Result-bearing lines can be large;
// 16MB matches the workflow reader's journal buffer.
const maxLineSize = 16 * 1024 * 1024

// loadedEntry is one usage-bearing transcript line with the fields needed for
// dedup, grouping, and cost. It is the Go analogue of ccusage's LoadedEntry.
type loadedEntry struct {
	SessionID   string // path-derived session id (ccusage grouping key)
	ProjectPath string // path-derived project slug
	MessageID   string
	RequestID   string
	IsSidechain bool
	Model       string
	Timestamp   time.Time
	Usage       transcript.Usage
}

// usageTokenTotal sums the four token classes for an entry's usage (the value
// ccusage compares when choosing which duplicate to keep).
func usageTokenTotal(u transcript.Usage) int64 {
	return int64(u.InputTokens) + int64(u.OutputTokens) +
		int64(cacheCreationTokenCount(u)) + int64(u.CacheReadInputTokens)
}

// cacheCreationTokenCount returns the cache-creation token count, preferring the
// flat field and falling back to the 5m+1h breakdown sum when the flat field is
// absent. Mirrors ccusage cache_creation_token_count.
func cacheCreationTokenCount(u transcript.Usage) int {
	if u.CacheCreationInputTokens != 0 {
		return u.CacheCreationInputTokens
	}
	if u.CacheCreation != nil {
		return u.CacheCreation.Ephemeral5mInputTokens + u.CacheCreation.Ephemeral1hInputTokens
	}
	return 0
}

// rawLine is the subset of a Claude JSONL line needed for usage accounting.
type rawLine struct {
	Type        string    `json:"type"`
	SessionID   string    `json:"sessionId"`
	RequestID   string    `json:"requestId"`
	IsSidechain bool      `json:"isSidechain"`
	Timestamp   time.Time `json:"timestamp"`
	Message     *struct {
		ID    string            `json:"id"`
		Model string            `json:"model"`
		Usage *transcript.Usage `json:"usage"`
	} `json:"message"`
}

// loadFileEntries reads a Claude JSONL transcript and returns its usage-bearing
// entries, tagged with the given path-derived sessionID/projectPath. Lines
// without message.usage are skipped (they carry no billing). Malformed lines are
// skipped for format-drift tolerance; only an open error is returned.
func loadFileEntries(path, sessionID, projectPath string) ([]loadedEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxLineSize)

	var entries []loadedEntry
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		// Cheap prefilter: only lines carrying usage data matter.
		if !strings.Contains(string(line), "\"usage\"") {
			continue
		}
		var raw rawLine
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}
		if raw.Message == nil || raw.Message.Usage == nil {
			continue
		}
		entries = append(entries, loadedEntry{
			SessionID:   sessionID,
			ProjectPath: projectPath,
			MessageID:   raw.Message.ID,
			RequestID:   raw.RequestID,
			IsSidechain: raw.IsSidechain,
			Model:       raw.Message.Model,
			Timestamp:   raw.Timestamp,
			Usage:       *raw.Message.Usage,
		})
	}
	if err := scanner.Err(); err != nil {
		return entries, err
	}
	return entries, nil
}

// FileStats is the per-file token rollup used by the tokens command: cumulative
// totals across every usage-bearing message (no dedup, matching the historical
// tokens output) plus the latest message's context-window figures.
type FileStats struct {
	MessageCount          int
	TotalInputTokens      int
	TotalOutputTokens     int
	TotalCacheCreation    int
	TotalCacheRead        int
	LatestContextSize     int
	LatestCacheReadTokens int
	LatestOutputTokens    int
}

// FileTokenStats reads a Claude JSONL transcript and returns cumulative token
// totals plus the latest message's context figures. It sums every usage-bearing
// message in file order (no dedup) so its totals match the historical `aglogs
// tokens` behaviour; latest_context_size is cache_read+cache_creation+input of
// the last usage line. Malformed lines are skipped; only an open error returns.
func FileTokenStats(path string) (FileStats, error) {
	f, err := os.Open(path)
	if err != nil {
		return FileStats{}, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxLineSize)

	var stats FileStats
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if !strings.Contains(string(line), "\"usage\"") {
			continue
		}
		var raw rawLine
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}
		if raw.Message == nil || raw.Message.Usage == nil {
			continue
		}
		u := raw.Message.Usage
		stats.MessageCount++
		stats.TotalInputTokens += u.InputTokens
		stats.TotalOutputTokens += u.OutputTokens
		stats.LatestOutputTokens = u.OutputTokens
		cacheCreation := cacheCreationTokenCount(*u)
		stats.TotalCacheCreation += cacheCreation
		stats.TotalCacheRead += u.CacheReadInputTokens
		stats.LatestCacheReadTokens = u.CacheReadInputTokens
		stats.LatestContextSize = u.CacheReadInputTokens + cacheCreation + u.InputTokens
	}
	if err := scanner.Err(); err != nil {
		return stats, err
	}
	return stats, nil
}

// innerSessionID reads the first sessionId field from a transcript file. Used to
// match a root agent-*.jsonl to its parent session (its inner sessionId equals
// the parent session id). Returns "" when none is found in the first lines.
func innerSessionID(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxLineSize)
	for i := 0; i < 50 && scanner.Scan(); i++ {
		var raw struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &raw); err == nil && raw.SessionID != "" {
			return raw.SessionID
		}
	}
	return ""
}
