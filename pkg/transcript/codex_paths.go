package transcript

import "path/filepath"

// CodexSessionsGlob returns the glob pattern matching Codex rollout transcript
// files under homeDir. Codex nests session files by date:
//
//	~/.codex/sessions/YYYY/MM/DD/rollout-<timestamp>-<uuid>.jsonl
//
// (see codex-rs/rollout/src/recorder.rs in the codex source). A non-empty
// sessionID narrows the match to filenames containing that id.
//
// This is the single definition of the codex session-file layout — discovery
// (pkg/agentstream), scanning (internal/session), and transcript path lookup
// (GetTranscriptPath) all share it rather than duplicating the glob.
func CodexSessionsGlob(homeDir, sessionID string) string {
	name := "*.jsonl"
	if sessionID != "" {
		name = "*" + sessionID + "*.jsonl"
	}
	return filepath.Join(homeDir, ".codex", "sessions", "*", "*", "*", name)
}
