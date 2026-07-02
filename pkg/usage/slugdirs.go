package usage

import (
	"os"
	"path/filepath"
)

// SlugDirsForSession returns the project-slug directories to scan for a Claude
// session's transcripts, so SummarizeSession can find the parent
// <session-id>.jsonl transcript, ad-hoc agent-*.jsonl files, and workflow
// subagent runs for the session. It is the shared resolver used by both
// flow (flow/pkg/orchestration.resolveTokenUsageSlugDirs) and the daemon
// session collector — the daemon MUST NOT import flow/pkg/orchestration, so the
// authoritative logic lives here.
//
// It globs every …/projects/*/<claudeSessionID> per-session directory and maps
// each up to its parent slug dir (…/<slug>/); the slug dir directly holding
// transcriptPath is always added as a fallback. An empty result makes
// SummarizeSession scan all of the Claude projects dir itself.
func SlugDirsForSession(claudeSessionID, transcriptPath string) []string {
	seen := make(map[string]bool)
	var slugDirs []string
	add := func(dir string) {
		if dir == "" || seen[dir] {
			return
		}
		seen[dir] = true
		slugDirs = append(slugDirs, dir)
	}

	for _, dir := range sessionDirsForID(claudeSessionID) {
		// dir is …/<slug>/<session-id>; the slug dir is its parent.
		add(filepath.Dir(dir))
	}

	// Fallback: the slug dir directly holding the parent transcript
	// (…/projects/<slug>/<session-id>.jsonl).
	if transcriptPath != "" {
		add(filepath.Dir(transcriptPath))
	}

	return slugDirs
}

// sessionDirsForID globs every …/projects/*/<claudeSessionID> per-session
// directory. It mirrors core sessions.ResolveClaudeSessionDirs but honors this
// package's claudeProjectsDir override (CLAUDE_CONFIG_DIR), keeping it in step
// with the rest of the usage summarizer and avoiding a core import here.
func sessionDirsForID(claudeSessionID string) []string {
	if claudeSessionID == "" {
		return nil
	}
	root, err := claudeProjectsDir()
	if err != nil {
		return nil
	}
	matches, err := filepath.Glob(filepath.Join(root, "*", claudeSessionID))
	if err != nil {
		return nil
	}
	var dirs []string
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && info.IsDir() {
			dirs = append(dirs, m)
		}
	}
	return dirs
}
