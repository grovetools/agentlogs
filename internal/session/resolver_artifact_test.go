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
	// The fast path must populate the project columns the scanner path filled.
	if got.ProjectPath != "/tmp/repo" || got.ProjectName != "repo" {
		t.Fatalf("project fields = %q / %q", got.ProjectPath, got.ProjectName)
	}
}

// enrichLogFilePath must resolve the job's own artifact transcript instead of
// falling through to a full multi-provider corpus scan.
func TestEnrichLogFilePathUsesJobArtifactDirectory(t *testing.T) {
	planDir := t.TempDir()
	jobID := "impl-456"
	sessionsDir := filepath.Join(planDir, ".artifacts", jobID, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessionsDir, "native.jsonl")
	data := `{"type":"session","id":"pi-native","timestamp":"2026-07-26T10:01:00Z","cwd":"/tmp/repo"}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	info := &SessionInfo{SessionID: jobID, Provider: "pi"}
	enrichLogFilePath(info, artifactSessionHints{
		planDirs: []string{planDir, filepath.Dir(filepath.Join(planDir, "02-impl.md"))},
		jobIDs:   []string{jobID},
	})
	if info.LogFilePath != path {
		t.Fatalf("LogFilePath = %q, want %q", info.LogFilePath, path)
	}
	// The daemon-provided identity must survive artifact enrichment.
	if info.SessionID != jobID || info.Provider != "pi" {
		t.Fatalf("enrichment clobbered session identity: %#v", info)
	}
}

// A session that already knows its transcript needs no resolution at all.
func TestEnrichLogFilePathKeepsExistingPath(t *testing.T) {
	info := &SessionInfo{SessionID: "s", LogFilePath: "/already/known.jsonl"}
	enrichLogFilePath(info, artifactSessionHints{planDirs: []string{t.TempDir()}, jobIDs: []string{"job"}})
	if info.LogFilePath != "/already/known.jsonl" {
		t.Fatalf("LogFilePath = %q", info.LogFilePath)
	}
}

// Empty/degenerate hints must not resolve anything (and must not be treated as
// a valid plan directory).
func TestArtifactSessionHintsIgnoreEmptyValues(t *testing.T) {
	hints := artifactSessionHints{
		planDirs: []string{"", ".", ""},
		jobIDs:   []string{"", ""},
	}
	if got := hints.resolve(); got != "" {
		t.Fatalf("resolve() = %q, want empty", got)
	}
}
