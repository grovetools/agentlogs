package agentstream

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	piToolCallLine   = `{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-07-01T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Running it."},{"type":"toolCall","id":"tc-9","name":"bash","arguments":{"command":"make build"}}]}}`
	piToolResultLine = `{"type":"message","id":"m3","parentId":"m2","timestamp":"2026-07-01T10:00:03.000Z","message":{"role":"toolResult","toolCallId":"tc-9","toolName":"bash","content":[{"type":"text","text":"ok"}],"isError":false}}`
	piUserLine       = `{"type":"message","id":"m1","parentId":null,"timestamp":"2026-07-01T10:00:01.000Z","message":{"role":"user","content":"build it"}}`
)

func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDeriveTranscriptStatus_InFlightToolRuns(t *testing.T) {
	// A tool_call with no matching tool_result marks the session running with
	// the tool named, even when the file is older than the recency window.
	path := writeTranscript(t, piUserLine, piToolCallLine)
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	status, err := DeriveTranscriptStatus(path, "pi", time.Now())
	if err != nil {
		t.Fatalf("DeriveTranscriptStatus: %v", err)
	}
	if status.State != "running" {
		t.Errorf("State = %q, want running (in-flight tool)", status.State)
	}
	if status.Activity != "tool: bash" {
		t.Errorf("Activity = %q, want 'tool: bash'", status.Activity)
	}
	// Pane-scrape fields stay zero for non-Claude providers.
	if status.TotalTokens != 0 || status.RawLine != "" || len(status.TodoItems) != 0 {
		t.Errorf("pane-scrape fields must stay zero: %+v", status)
	}
}

func TestDeriveTranscriptStatus_QuietTranscriptIdle(t *testing.T) {
	// Resolved tool call + stale mtime → idle.
	path := writeTranscript(t, piUserLine, piToolCallLine, piToolResultLine)
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	status, err := DeriveTranscriptStatus(path, "pi", time.Now())
	if err != nil {
		t.Fatalf("DeriveTranscriptStatus: %v", err)
	}
	if status.State != "idle" {
		t.Errorf("State = %q, want idle", status.State)
	}
}

func TestDeriveTranscriptStatus_RecentGrowthRuns(t *testing.T) {
	// A freshly-written transcript is running even with all tools resolved.
	path := writeTranscript(t, piUserLine, piToolCallLine, piToolResultLine)
	status, err := DeriveTranscriptStatus(path, "pi", time.Now())
	if err != nil {
		t.Fatalf("DeriveTranscriptStatus: %v", err)
	}
	if status.State != "running" {
		t.Errorf("State = %q, want running (recent mtime)", status.State)
	}
}

func TestStatusFromPane_ProviderGuard(t *testing.T) {
	pane := "✶ Unravelling… (esc to interrupt · 8s · ↓ 220 tokens · thinking)\n"
	if StatusFromPane(pane, "claude") == nil {
		t.Error("claude pane parse should succeed")
	}
	if StatusFromPane(pane, "") == nil {
		t.Error("empty provider defaults to claude")
	}
	for _, p := range []string{"codex", "opencode", "pi"} {
		if got := StatusFromPane(pane, p); got != nil {
			t.Errorf("provider %s: pane scrape must be nil, got %+v", p, got)
		}
	}
}
