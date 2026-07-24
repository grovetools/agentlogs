package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/grovetools/core/pkg/sessions"
)

func TestSessionFromRegistryMetadataUsesExplicitTranscriptPath(t *testing.T) {
	transcriptPath := filepath.Join(t.TempDir(), "sessions", "native-pi-session.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcriptPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	startedAt := time.Date(2026, 7, 24, 8, 3, 25, 0, time.UTC)
	metadata := sessions.SessionMetadata{
		SessionID:        "review-work-flow-id",
		ClaudeSessionID:  "native-pi-id",
		Provider:         "pi",
		PID:              89820,
		Status:           "completed",
		WorkingDirectory: t.TempDir(),
		StartedAt:        startedAt,
		TranscriptPath:   transcriptPath,
		PlanName:         "agent-testing-environments",
		JobFilePath:      "/plans/agent-testing-environments/94-review-work.md",
	}

	info, ok := NewScannerWithoutDaemon().sessionFromRegistryMetadata("native-pi-id", metadata)
	if !ok {
		t.Fatal("expected registry transcript to be materialized")
	}
	if info.SessionID != "native-pi-id" {
		t.Errorf("SessionID = %q, want native-pi-id", info.SessionID)
	}
	if info.LogFilePath != transcriptPath {
		t.Errorf("LogFilePath = %q, want %q", info.LogFilePath, transcriptPath)
	}
	if info.Provider != "pi" {
		t.Errorf("Provider = %q, want pi", info.Provider)
	}
	if len(info.Jobs) != 1 || info.Jobs[0].Plan != "agent-testing-environments" || info.Jobs[0].Job != "94-review-work.md" {
		t.Errorf("Jobs = %+v", info.Jobs)
	}
	if info.StartedAt != startedAt || info.Status != "completed" || info.PID != 89820 {
		t.Errorf("runtime metadata not preserved: %+v", info)
	}
}

func TestSessionFromRegistryMetadataRejectsMissingTranscript(t *testing.T) {
	metadata := sessions.SessionMetadata{
		ClaudeSessionID: "native-pi-id",
		TranscriptPath:  filepath.Join(t.TempDir(), "missing.jsonl"),
	}

	if info, ok := NewScannerWithoutDaemon().sessionFromRegistryMetadata("native-pi-id", metadata); ok {
		t.Fatalf("expected stale registry record to be rejected, got %+v", info)
	}
}
