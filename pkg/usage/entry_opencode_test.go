package usage

import (
	"path/filepath"
	"testing"
)

func TestOpencodeFileTokenStats(t *testing.T) {
	// The routed path is the session info file inside the fragment store —
	// exactly what the scanner sets as LogFilePath and what the plugin's
	// metadata pointer resolves to.
	path := filepath.Join("testdata", "opencode", "storage", "session", "proj_t", "ses_tok00001.json")

	stats, err := FileTokenStatsForProvider(path, "opencode")
	if err != nil {
		t.Fatalf("FileTokenStatsForProvider: %v", err)
	}

	// Two usage-bearing assistant messages; the user message carries none.
	if stats.MessageCount != 2 {
		t.Errorf("MessageCount = %d, want 2", stats.MessageCount)
	}
	if stats.TotalInputTokens != 130 {
		t.Errorf("TotalInputTokens = %d, want 130", stats.TotalInputTokens)
	}
	if stats.TotalOutputTokens != 100 {
		t.Errorf("TotalOutputTokens = %d, want 100", stats.TotalOutputTokens)
	}
	if stats.TotalCacheRead != 550 {
		t.Errorf("TotalCacheRead = %d, want 550", stats.TotalCacheRead)
	}
	if stats.TotalCacheCreation != 70 {
		t.Errorf("TotalCacheCreation = %d, want 70", stats.TotalCacheCreation)
	}

	// Latest figures come from msg_0003: context = input + cache_read +
	// cache_write (opencode's input excludes cache).
	if stats.LatestOutputTokens != 60 {
		t.Errorf("LatestOutputTokens = %d, want 60", stats.LatestOutputTokens)
	}
	if stats.LatestCacheReadTokens != 350 {
		t.Errorf("LatestCacheReadTokens = %d, want 350", stats.LatestCacheReadTokens)
	}
	if wantCtx := 30 + 350 + 20; stats.LatestContextSize != wantCtx {
		t.Errorf("LatestContextSize = %d, want %d", stats.LatestContextSize, wantCtx)
	}
}

func TestOpencodeFileTokenStatsUnknownSession(t *testing.T) {
	path := filepath.Join("testdata", "opencode", "storage", "session", "proj_t", "ses_missing.json")
	if _, err := FileTokenStatsForProvider(path, "opencode"); err == nil {
		t.Fatal("expected error for session without message fragments")
	}
}
