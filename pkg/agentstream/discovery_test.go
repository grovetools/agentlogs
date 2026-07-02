package agentstream

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeCodexSession writes a rollout file at the given date-nested path under
// the fake home and stamps its mtime.
func writeCodexSession(t *testing.T, home, datePath, name string, modTime time.Time) string {
	t.Helper()
	dir := filepath.Join(home, ".codex", "sessions", filepath.FromSlash(datePath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(`{"type":"session_meta","payload":{}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestDiscoverTranscript_CodexNestedLayout covers codex's real session layout:
// ~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl (the old flat glob
// never matched anything).
func TestDiscoverTranscript_CodexNestedLayout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	older := writeCodexSession(t, home, "2026/06/30",
		"rollout-2026-06-30T09-00-00-aaaaaaaa-1111-2222-3333-444444444444.jsonl",
		time.Now().Add(-2*time.Hour))
	newest := writeCodexSession(t, home, "2026/07/01",
		"rollout-2026-07-01T10-00-00-bbbbbbbb-1111-2222-3333-444444444444.jsonl",
		time.Now().Add(-1*time.Minute))

	got, err := DiscoverTranscript(DiscoverOptions{Provider: "codex", WorkDir: "/tmp"})
	if err != nil {
		t.Fatalf("DiscoverTranscript: %v", err)
	}
	if got != newest {
		t.Errorf("got %s, want newest %s", got, newest)
	}

	// AfterTime filters out sessions that predate the launch.
	got, err = DiscoverTranscript(DiscoverOptions{
		Provider:  "codex",
		AfterTime: time.Now().Add(-30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("DiscoverTranscript with AfterTime: %v", err)
	}
	if got != newest {
		t.Errorf("AfterTime discovery got %s, want %s", got, newest)
	}
	_ = older

	// A cutoff after every session yields an error, not a stale match.
	if _, err := DiscoverTranscript(DiscoverOptions{
		Provider:  "codex",
		AfterTime: time.Now(),
	}); err == nil {
		t.Error("expected error when no session is newer than AfterTime")
	}
}

func TestDiscoverTranscript_CodexIgnoresFlatFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Codex never writes flat files under sessions/; a stray one must not match.
	flatDir := filepath.Join(home, ".codex", "sessions")
	if err := os.MkdirAll(flatDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(flatDir, "stray.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := DiscoverTranscript(DiscoverOptions{Provider: "codex"}); err == nil {
		t.Error("expected error when only flat (non-nested) files exist")
	}

	nested := writeCodexSession(t, home, "2026/07/01",
		"rollout-2026-07-01T10-00-00-cccccccc-1111-2222-3333-444444444444.jsonl",
		time.Now())
	got, err := DiscoverTranscript(DiscoverOptions{Provider: "codex"})
	if err != nil {
		t.Fatalf("DiscoverTranscript: %v", err)
	}
	if got != nested {
		t.Errorf("got %s, want nested %s", got, nested)
	}
}

// writePiSession writes a pi session file into the munged-cwd session dir
// under the fake home. headerTime goes into the header line's timestamp field
// (discovery prefers content time over mtime).
func writePiSession(t *testing.T, home, workDir, name string, headerTime time.Time) string {
	t.Helper()
	dir := filepath.Join(home, ".pi", "agent", "sessions", "--"+strings.ReplaceAll(strings.TrimPrefix(workDir, "/"), "/", "-")+"--")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	header := `{"type":"session","version":3,"id":"` + strings.TrimSuffix(name, ".jsonl") + `","timestamp":"` + headerTime.UTC().Format(time.RFC3339Nano) + `","cwd":"` + workDir + `"}` + "\n"
	if err := os.WriteFile(path, []byte(header), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestDiscoverTranscript_PiMungedCwdLayout covers pi's per-cwd session layout:
// ~/.pi/agent/sessions/--<cwd-with-slashes-as-dashes>--/<ts>_<uuid>.jsonl.
func TestDiscoverTranscript_PiMungedCwdLayout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workDir := "/Users/test/project"

	older := writePiSession(t, home, workDir,
		"2026-07-01T08-00-00-000Z_aaaaaaaa-1111-7222-3333-444444444444.jsonl",
		time.Now().Add(-2*time.Hour))
	newest := writePiSession(t, home, workDir,
		"2026-07-01T10-00-00-000Z_bbbbbbbb-1111-7222-3333-444444444444.jsonl",
		time.Now().Add(-1*time.Minute))
	// A session for a DIFFERENT cwd must never match this workDir.
	otherCwd := writePiSession(t, home, "/Users/test/other",
		"2026-07-01T10-30-00-000Z_cccccccc-1111-7222-3333-444444444444.jsonl",
		time.Now())

	got, err := DiscoverTranscript(DiscoverOptions{Provider: "pi", WorkDir: workDir})
	if err != nil {
		t.Fatalf("DiscoverTranscript: %v", err)
	}
	if got != newest {
		t.Errorf("got %s, want newest %s", got, newest)
	}
	if got == otherCwd {
		t.Error("matched a session from a different cwd")
	}

	// AfterTime filters out sessions that predate the launch.
	got, err = DiscoverTranscript(DiscoverOptions{
		Provider:  "pi",
		WorkDir:   workDir,
		AfterTime: time.Now().Add(-30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("DiscoverTranscript with AfterTime: %v", err)
	}
	if got != newest {
		t.Errorf("AfterTime discovery got %s, want %s", got, newest)
	}
	_ = older

	// A cutoff after every session yields an error, not a stale match.
	if _, err := DiscoverTranscript(DiscoverOptions{
		Provider:  "pi",
		WorkDir:   workDir,
		AfterTime: time.Now(),
	}); err == nil {
		t.Error("expected error when no pi session is newer than AfterTime")
	}
}

func TestDiscoverTranscript_OpencodeNotImplemented(t *testing.T) {
	_, err := DiscoverTranscript(DiscoverOptions{Provider: "opencode", WorkDir: "/tmp"})
	if err == nil {
		t.Fatal("expected error for opencode transcript discovery, got nil")
	}
	if !errors.Is(err, ErrUnsupportedProvider) {
		t.Errorf("expected ErrUnsupportedProvider, got: %v", err)
	}
	for _, want := range []string{"opencode", "claude", "codex"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func TestDiscoverTranscript_UnknownProvider(t *testing.T) {
	_, err := DiscoverTranscript(DiscoverOptions{Provider: "gemini"})
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
	if !errors.Is(err, ErrUnsupportedProvider) {
		t.Errorf("expected ErrUnsupportedProvider, got: %v", err)
	}
	if !strings.Contains(err.Error(), "gemini") {
		t.Errorf("error should name the provider, got: %v", err)
	}
}
