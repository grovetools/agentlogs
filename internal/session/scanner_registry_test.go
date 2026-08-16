package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/grovetools/core/pkg/sessions"
)

func TestLoadSessionRegistryMapsAttemptKeyedRecordByNativeAlias(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	registryRoot := filepath.Join(stateHome, "grove", "hooks", "sessions")

	write := func(dir string, metadata sessions.SessionMetadata) {
		t.Helper()
		path := filepath.Join(registryRoot, dir, "metadata.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(metadata)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("019d-attempt", sessions.SessionMetadata{
		AttemptID:       "019d-attempt",
		SessionID:       "reused-job-id",
		JobID:           "reused-job-id",
		ClaudeSessionID: "native-current",
		StartedAt:       time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC),
	})
	// A stale legacy record for a resumed native alias must not replace the
	// exact current attempt merely because its directory sorts later.
	write("zz-native-current", sessions.SessionMetadata{
		SessionID:       "reused-job-id",
		ClaudeSessionID: "native-current",
		StartedAt:       time.Date(2026, 8, 15, 13, 0, 0, 0, time.UTC),
	})
	// Legacy native-keyed records remain readable during GC migration.
	write("native-legacy", sessions.SessionMetadata{
		SessionID:       "legacy-job",
		ClaudeSessionID: "native-legacy",
	})

	got, err := NewScannerWithoutDaemon().loadSessionRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if current, ok := got["native-current"]; !ok || current.AttemptID != "019d-attempt" || current.JobID != "reused-job-id" {
		t.Fatalf("attempt-keyed record missing by native alias: %+v", current)
	}
	if legacy, ok := got["native-legacy"]; !ok || legacy.SessionID != "legacy-job" {
		t.Fatalf("legacy native-keyed record missing: %+v", legacy)
	}
}

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
