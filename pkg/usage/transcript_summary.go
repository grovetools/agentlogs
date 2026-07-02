package usage

import (
	"path/filepath"
	"strings"

	"github.com/grovetools/agentlogs/internal/opencode"
)

// SummarizeSessionTranscript summarizes one session's usage + cost from its
// resolved transcript locator, routed by provider. This is the seam callers
// with a (path, provider) pair — flow's per-job token accounting for
// non-Claude sessions — use instead of the Claude-only SummarizeSession
// (which additionally discovers ad-hoc/workflow subagent files; those trees
// only exist for Claude).
//
// Path semantics per provider:
//   - "claude" (or ""): a ~/.claude/projects/<slug>/<id>.jsonl transcript.
//     Parsed with the exact loader/dedup/summarize chain the Claude scan
//     uses; prefer SummarizeSession when subagent rollup matters.
//   - "codex": a rollout JSONL file (nested ~/.codex/sessions layout).
//   - "pi": a session JSONL file (~/.pi/agent/sessions/--<cwd>--/).
//   - "opencode": the session INFO file <storage>/session/<projectID>/
//     <ses_...>.json — opencode has no transcript file; the storage root and
//     native session id are derived from that path and the messages read
//     through the fragment assembler (same derivation as
//     opencodeFileTokenStats).
func SummarizeSessionTranscript(path, provider string, mode CostMode) (Summary, error) {
	pm := DefaultPricing()

	var entries []loadedEntry
	var sessionID, projectPath string
	var err error

	switch provider {
	case "codex":
		entries, err = codexTranscriptEntries(path)
	case "pi":
		entries, err = piTranscriptEntries(path)
	case "opencode":
		entries, err = opencodeTranscriptEntries(path)
	default: // claude and unknown: the historical Claude-shaped parsing
		sessionID, projectPath = extractSessionParts(path)
		entries, err = loadFileEntries(path, sessionID, projectPath)
	}
	if err != nil {
		return Summary{}, err
	}
	if sessionID == "" && len(entries) > 0 {
		sessionID = entries[0].SessionID
		projectPath = entries[0].ProjectPath
	}

	entries = dedupe(entries)
	return summarize(sessionID, projectPath, entries, nil, mode, pm), nil
}

// opencodeTranscriptEntries loads one opencode session's usage entries from
// its session info file path: <storage>/session/<projectID>/<ses_...>.json.
func opencodeTranscriptEntries(path string) ([]loadedEntry, error) {
	sessionID := strings.TrimSuffix(filepath.Base(path), ".json")
	// <storage>/session/<projectID>/<ses_...>.json -> <storage>
	storageDir := filepath.Dir(filepath.Dir(filepath.Dir(path)))
	assembler, err := opencode.NewAssemblerWithDir(storageDir)
	if err != nil {
		return nil, err
	}
	projectPath := opencodeSessionDirectory(path)
	if projectPath == "" {
		projectPath = filepath.Base(filepath.Dir(path))
	}
	return opencodeSessionEntries(assembler, sessionID, projectPath)
}
