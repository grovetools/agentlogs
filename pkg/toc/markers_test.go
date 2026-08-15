package toc

import "testing"

func testMarkers() PathMarkers {
	return PathMarkers{
		Worktree:  "/wt/misc-fixes",
		Notebook:  "/nb/workspaces/grovetools",
		Artifacts: "/nb/workspaces/grovetools/plans/misc-fixes/.artifacts/my-job",
	}
}

func TestPathMarkersLongestDirectoryWins(t *testing.T) {
	m := testMarkers()
	cases := []struct {
		path, token, rest string
	}{
		// The artifacts dir is nested inside the notebook, so a shortest-match
		// rule would render every briefing under $NB and $JA would never fire.
		{"/nb/workspaces/grovetools/plans/misc-fixes/.artifacts/my-job/briefing-1785319535.xml", ArtifactsMarker, "briefing-1785319535.xml"},
		{"/nb/workspaces/grovetools/inbox/20260729-test.md", NotebookMarker, "inbox/20260729-test.md"},
		{"/nb/workspaces/grovetools/plans/misc-fixes/07-job.md", NotebookMarker, "plans/misc-fixes/07-job.md"},
		{"/wt/misc-fixes/treemux/internal/toc/files.go", WorktreeMarker, "treemux/internal/toc/files.go"},
	}
	for _, tt := range cases {
		token, rest, exact, ok := m.Match(tt.path)
		if !ok || exact || token != tt.token || rest != tt.rest {
			t.Errorf("Match(%q) = %q/%q exact=%v ok=%v, want %q/%q", tt.path, token, rest, exact, ok, tt.token, tt.rest)
		}
	}
}

func TestPathMarkersReportsExactDirectoryAndMisses(t *testing.T) {
	m := testMarkers()
	if token, rest, exact, ok := m.Match("/wt/misc-fixes"); !ok || !exact || token != WorktreeMarker || rest != "" {
		t.Fatalf("exact match = %q/%q exact=%v ok=%v", token, rest, exact, ok)
	}
	// A sibling job's artifacts and another workspace's notebook are not this
	// session's, and a path outside every marker is nobody's.
	for _, path := range []string{
		"/nb/workspaces/tuimux/concepts/overview.md",
		"/Users/solair/Code/solutils/nanob/Makefile",
		"/wt/other-plan/treemux/x.go",
	} {
		if token, _, _, ok := m.Match(path); ok {
			t.Errorf("Match(%q) claimed %q, want no marker", path, token)
		}
	}
}

// A near-miss must not match: /wt/misc-fixes-2 shares a textual prefix with
// /wt/misc-fixes but is a different directory.
func TestPathMarkersRequiresASegmentBoundary(t *testing.T) {
	m := testMarkers()
	if token, _, _, ok := m.Match("/wt/misc-fixes-2/treemux/x.go"); ok {
		t.Fatalf("sibling directory matched %q", token)
	}
}

// The zero value substitutes nothing — the correct behavior for a session whose
// directories are unknown, and the reason an ad-hoc agent's paths render whole.
func TestPathMarkersZeroValueMatchesNothing(t *testing.T) {
	if _, _, _, ok := (PathMarkers{}).Match("/anything/at/all"); ok {
		t.Fatal("zero-value markers matched")
	}
	// A directory no longer than its own token buys nothing by being replaced.
	if _, _, _, ok := (PathMarkers{Worktree: "/a"}).Match("/a/b.go"); ok {
		t.Fatal("a directory shorter than its token was substituted")
	}
}

// The outline compacts through the same vocabulary the files pane renders, so a
// path reads the same in both panes.
func TestCompactPathUsesTheSharedMarkers(t *testing.T) {
	m := testMarkers()
	cases := []struct{ in, want string }{
		{"/nb/workspaces/grovetools/plans/misc-fixes/.artifacts/my-job/briefing-1785319535.xml", ArtifactsMarker + "/briefing-1785319535.xml"},
		{"/nb/workspaces/grovetools/inbox/20260729-test.md", NotebookMarker + "/inbox/20260729-test.md"},
		{"/wt/misc-fixes", WorktreeMarker},
		// Deeper than the tail budget still elides, marker and all.
		{"/wt/misc-fixes/treemux/internal/toc/files.go", WorktreeMarker + "/.../toc/files.go"},
		// No marker and nothing worth eliding: left alone.
		{"/etc/hosts", "/etc/hosts"},
	}
	for _, tt := range cases {
		if got := compactPath(tt.in, m); got != tt.want {
			t.Errorf("compactPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCompactTitlePathsRewritesEveryPathInARow(t *testing.T) {
	got := compactTitlePaths("Read /nb/workspaces/grovetools/inbox/20260729-test.md and /wt/misc-fixes/go.work", testMarkers())
	want := "Read " + NotebookMarker + "/inbox/20260729-test.md and " + WorktreeMarker + "/go.work"
	if got != want {
		t.Fatalf("compactTitlePaths = %q, want %q", got, want)
	}
}
