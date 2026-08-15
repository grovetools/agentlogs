package toc

import "strings"

// Path markers are the outline's shared vocabulary for "this long prefix is the
// same on every row and you already know what it is". One token per place a
// tracked agent session habitually reads and writes.
//
// They are kept two or three cells wide deliberately. A marker is only worth
// substituting if it is affordable on EVERY row of a 35-column drawer —
// otherwise it buys nothing over the elision it replaces and the substitution
// has to be applied selectively, which defeats the point of a fixed vocabulary.
//
// Every pane that renders agent paths uses these tokens for the same things, so
// a path reads the same in the outline as it does in an accessed-files list.
// What differs is the FALLBACK when no marker matches: the outline elides to
// the path's tail, while other panes may root against their own context.
const (
	// WorktreeMarker stands in for the agent's own working directory. Renderers
	// color the token so an elided prefix cannot be misread as a relative path
	// the agent actually typed.
	WorktreeMarker = "$WT"

	// ArtifactsMarker stands in for the tracked job's .artifacts directory: the
	// longest shared prefix in the whole list and the least informative, since
	// every briefing, checklist and transcript a job reads lives under one
	// directory named after the job's own ID.
	//
	// Display only. A successor agent's artifacts directory is a DIFFERENT
	// directory, so a copied $JA path would silently resolve to the wrong job.
	ArtifactsMarker = "$JA"

	// NotebookMarker stands in for the workspace's own notebook: where its
	// inbox, plans, concepts and completed notes live. Like $JA it is display
	// only — another workspace has another notebook.
	NotebookMarker = "$NB"
)

// MarkerTokens is every marker a rendered path can carry, for renderers that
// need to find and style them. Fresh slice per call.
func MarkerTokens() []string {
	return []string{WorktreeMarker, ArtifactsMarker, NotebookMarker}
}

// PathMarkers binds the marker tokens to one tracked session's actual
// directories. The zero value substitutes nothing, which is the correct
// behavior for a session whose directories are unknown.
type PathMarkers struct {
	// Worktree is the session's working directory ($WT).
	Worktree string
	// Artifacts is the session's own job-artifacts directory ($JA).
	Artifacts string
	// Notebook is the workspace notebook that owns the session's plan ($NB).
	Notebook string
}

// Match resolves path against the markers, returning the winning token, the
// path relative to that marker's directory, and whether the path IS the
// directory rather than something under it.
//
// The LONGEST matching directory wins, which is load-bearing rather than a
// tie-break nicety: the artifacts directory is nested inside the plan, which is
// nested inside the notebook, so a shortest-match rule would render every
// briefing as $NB/plans/<slug>/.artifacts/<job>/briefing.xml and the more
// specific marker would never fire at all.
func (m PathMarkers) Match(path string) (token, rest string, exact, ok bool) {
	longest := 0
	for _, candidate := range []struct{ token, dir string }{
		{WorktreeMarker, m.Worktree},
		{ArtifactsMarker, m.Artifacts},
		{NotebookMarker, m.Notebook},
	} {
		dir := strings.TrimRight(candidate.dir, "/")
		// A directory no longer than the token replacing it buys nothing, and a
		// shorter match than one already found cannot be more specific.
		if dir == "" || dir == "/" || len(dir) <= len(candidate.token) || len(dir) <= longest {
			continue
		}
		if path == dir {
			token, rest, exact, ok, longest = candidate.token, "", true, true, len(dir)
			continue
		}
		if suffix, found := strings.CutPrefix(path, dir+"/"); found {
			token, rest, exact, ok, longest = candidate.token, suffix, false, true, len(dir)
		}
	}
	return token, rest, exact, ok
}
