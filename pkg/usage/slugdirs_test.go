package usage

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSlugDirsForSession verifies the shared resolver maps a session's
// per-session dirs up to their slug parents and always includes the transcript's
// own slug dir as a fallback, deduped.
func TestSlugDirsForSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	projects := filepath.Join(root, "projects")
	sid := "sess-xyz"

	// Two slug dirs both holding a per-session subdir for this session id.
	slugA := filepath.Join(projects, "-proj-a")
	slugB := filepath.Join(projects, "-proj-b")
	if err := os.MkdirAll(filepath.Join(slugA, sid), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(slugB, sid), 0o755); err != nil {
		t.Fatal(err)
	}

	// Transcript lives directly under slugA (its parent slug dir == slugA).
	transcript := filepath.Join(slugA, sid+".jsonl")

	got := SlugDirsForSession(sid, transcript)

	want := map[string]bool{slugA: false, slugB: false}
	for _, d := range got {
		if _, ok := want[d]; !ok {
			t.Errorf("unexpected slug dir %q", d)
			continue
		}
		want[d] = true
	}
	for d, seen := range want {
		if !seen {
			t.Errorf("missing expected slug dir %q (got %v)", d, got)
		}
	}
	// slugA appears via both the glob and the transcript fallback but must be
	// deduped to a single entry.
	count := 0
	for _, d := range got {
		if d == slugA {
			count++
		}
	}
	if count != 1 {
		t.Errorf("slugA appeared %d times, want 1 (dedup failed): %v", count, got)
	}
}

// TestSlugDirsForSession_TranscriptOnly verifies that with no matching
// per-session dirs, the transcript's slug dir is still returned.
func TestSlugDirsForSession_TranscriptOnly(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	slug := filepath.Join(root, "projects", "-proj")
	transcript := filepath.Join(slug, "no-match.jsonl")

	got := SlugDirsForSession("absent-session", transcript)
	if len(got) != 1 || got[0] != slug {
		t.Errorf("got %v, want [%s]", got, slug)
	}
}
