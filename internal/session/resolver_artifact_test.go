package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/grovetools/core/pkg/models"
)

func TestResolveFlowArtifactSessionPrefersJobSessionsDirectory(t *testing.T) {
	planDir := t.TempDir()
	job := &models.JobInfo{
		ID:          "impl-123",
		Type:        "interactive_agent",
		PlanDir:     planDir,
		JobFile:     "02-impl.md",
		SubmittedAt: time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC),
	}
	sessionsDir := filepath.Join(planDir, ".artifacts", job.ID, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessionsDir, "native.jsonl")
	data := `{"type":"session","id":"pi-native","timestamp":"2026-07-26T10:01:00Z","cwd":"/tmp/repo"}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	got := resolveFlowArtifactSession(job)
	if got == nil {
		t.Fatal("artifact session not resolved")
	}
	if got.SessionID != "pi-native" || got.LogFilePath != path || got.Provider != "pi" {
		t.Fatalf("got %#v", got)
	}
	if len(got.Jobs) != 1 || got.Jobs[0].Job != job.JobFile {
		t.Fatalf("jobs = %#v", got.Jobs)
	}
}
