package cmd

import "testing"

func TestProviderForLogPath(t *testing.T) {
	tests := map[string]string{
		"/home/user/.claude/projects/p/session.jsonl":         "claude",
		"/home/user/.codex/sessions/2026/07/24/session.jsonl": "codex",
		"/home/user/.pi/agent/sessions/project/session.jsonl": "pi",
	}
	for path, want := range tests {
		if got := providerForLogPath(path); got != want {
			t.Errorf("providerForLogPath(%q) = %q, want %q", path, got, want)
		}
	}
}
