package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/grovetools/core/pkg/sessions"
)

func TestScanArchivedSessionsInPlansDirFindsNestedPlanArtifacts(t *testing.T) {
	plansDir := t.TempDir()
	startedAt := time.Date(2026, 7, 24, 8, 3, 31, 0, time.UTC)
	jobDir := filepath.Join(plansDir, "agent-testing-environments", ".artifacts", "review-work-96f0352b")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}

	metadata := sessions.SessionMetadata{
		SessionID:        "review-work-96f0352b",
		ClaudeSessionID:  "019f9326-908b-7dce-a3e4-80992d766003",
		Provider:         "pi",
		WorkingDirectory: "/tmp/worktree",
		StartedAt:        startedAt,
		PlanName:         "agent-testing-environments",
		JobFilePath:      filepath.Join(plansDir, "agent-testing-environments", "94-review-work.md"),
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "metadata.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	transcriptPath := filepath.Join(jobDir, "transcript.jsonl")
	if err := os.WriteFile(transcriptPath, []byte("{\"type\":\"session\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := NewScannerWithoutDaemon().scanArchivedSessionsInPlansDir(plansDir)
	if len(got) != 1 {
		t.Fatalf("got %d archived sessions, want 1: %#v", len(got), got)
	}
	if got[0].SessionID != metadata.ClaudeSessionID {
		t.Errorf("SessionID = %q, want %q", got[0].SessionID, metadata.ClaudeSessionID)
	}
	if got[0].LogFilePath != transcriptPath {
		t.Errorf("LogFilePath = %q, want %q", got[0].LogFilePath, transcriptPath)
	}
	if got[0].Provider != "pi" {
		t.Errorf("Provider = %q, want pi", got[0].Provider)
	}
	if len(got[0].Jobs) != 1 || got[0].Jobs[0].Plan != metadata.PlanName || got[0].Jobs[0].Job != "94-review-work.md" {
		t.Errorf("Jobs = %#v, want plan/job from metadata", got[0].Jobs)
	}
}

func TestScanArchivedSessionsInPlanDirSkipsMissingTranscript(t *testing.T) {
	planDir := t.TempDir()
	jobDir := filepath.Join(planDir, ".artifacts", "missing-transcript")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metadata := sessions.SessionMetadata{SessionID: "missing-transcript", PlanName: "plan", JobFilePath: "job.md"}
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "metadata.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	got := NewScannerWithoutDaemon().scanArchivedSessionsInPlanDir(planDir)
	if len(got) != 0 {
		t.Fatalf("got %#v, want no unreadable archived sessions", got)
	}
}
