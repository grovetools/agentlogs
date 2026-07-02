package usage

import (
	"os"
	"path/filepath"
	"testing"
)

// codexFixture reuses the transcript package's real-shaped codex rollout
// fixture (nested ~/.codex/sessions/YYYY/MM/DD layout).
const codexFixture = "../transcript/testdata/codex/sessions/2026/07/01/rollout-2026-07-01T10-00-00-5973b6c0-94b8-487b-a530-2aeb6098ae0e.jsonl"

func TestFileTokenStatsForProvider_Codex(t *testing.T) {
	stats, err := FileTokenStatsForProvider(filepath.FromSlash(codexFixture), "codex")
	if err != nil {
		t.Fatalf("FileTokenStatsForProvider: %v", err)
	}

	// Two usage-bearing token_count events (the rate-limit-only info:null
	// event is not a usage message).
	if stats.MessageCount != 2 {
		t.Errorf("MessageCount = %d, want 2", stats.MessageCount)
	}
	// Totals come from the final event's cumulative total_token_usage:
	// input 3200 (2600 cached) → 600 fresh input + 2600 cache read.
	if stats.TotalInputTokens != 600 {
		t.Errorf("TotalInputTokens = %d, want 600", stats.TotalInputTokens)
	}
	if stats.TotalCacheRead != 2600 {
		t.Errorf("TotalCacheRead = %d, want 2600", stats.TotalCacheRead)
	}
	if stats.TotalOutputTokens != 450 {
		t.Errorf("TotalOutputTokens = %d, want 450", stats.TotalOutputTokens)
	}
	if stats.TotalCacheCreation != 0 {
		t.Errorf("TotalCacheCreation = %d, want 0 (codex has no cache-write class)", stats.TotalCacheCreation)
	}
	// Latest figures come from the final event's last_token_usage:
	// input 2000 (1600 cached), output 300.
	if stats.LatestOutputTokens != 300 {
		t.Errorf("LatestOutputTokens = %d, want 300", stats.LatestOutputTokens)
	}
	if stats.LatestCacheReadTokens != 1600 {
		t.Errorf("LatestCacheReadTokens = %d, want 1600", stats.LatestCacheReadTokens)
	}
	// Context size = full prompt of the last turn = input_tokens (2000).
	if stats.LatestContextSize != 2000 {
		t.Errorf("LatestContextSize = %d, want 2000", stats.LatestContextSize)
	}
}

func TestFileTokenStatsForProvider_ClaudeUnchanged(t *testing.T) {
	// The claude path must keep parsing the message.usage line shape.
	dir := t.TempDir()
	path := filepath.Join(dir, "claude.jsonl")
	content := `{"type":"assistant","timestamp":"2026-07-01T10:00:00Z","message":{"id":"msg_1","model":"claude-fable-5","usage":{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":30,"cache_read_input_tokens":40}}}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	stats, err := FileTokenStatsForProvider(path, "claude")
	if err != nil {
		t.Fatalf("FileTokenStatsForProvider: %v", err)
	}
	if stats.MessageCount != 1 || stats.TotalInputTokens != 10 || stats.TotalOutputTokens != 20 ||
		stats.TotalCacheCreation != 30 || stats.TotalCacheRead != 40 {
		t.Errorf("claude stats changed: %+v", stats)
	}
	if stats.LatestContextSize != 80 {
		t.Errorf("LatestContextSize = %d, want 80", stats.LatestContextSize)
	}
}

func TestFileTokenStatsForProvider_CodexFileWithoutUsage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-empty.jsonl")
	content := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	stats, err := FileTokenStatsForProvider(path, "codex")
	if err != nil {
		t.Fatalf("FileTokenStatsForProvider: %v", err)
	}
	if stats != (FileStats{}) {
		t.Errorf("expected zero stats for usage-less rollout, got %+v", stats)
	}
}
