package transcript

import (
	"path/filepath"
	"strings"
)

// PiSessionDirName converts a working directory into pi's per-cwd session
// subdirectory name. pi encodes the (resolved) cwd by stripping the single
// leading path separator, replacing every "/", "\" and ":" with "-", and
// wrapping the result in leading/trailing "--"
// (getDefaultSessionDirPath in packages/coding-agent/src/core/
// session-manager.ts of the pi source):
//
//	/Users/foo/bar -> --Users-foo-bar--
//
// Callers should pass an already-resolved absolute path (pi resolves the cwd
// before encoding; flow canonicalizes agent cwds the same way).
func PiSessionDirName(workDir string) string {
	trimmed := workDir
	if len(trimmed) > 0 && (trimmed[0] == '/' || trimmed[0] == '\\') {
		trimmed = trimmed[1:]
	}
	munged := strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(trimmed)
	return "--" + munged + "--"
}

// PiSessionsDir returns pi's session directory for a working directory:
//
//	~/.pi/agent/sessions/--<munged-cwd>--
//
// (getSessionsDir in packages/coding-agent/src/config.ts + the munging above.
// pi honors a PI_CODING_AGENT_DIR env override of ~/.pi/agent; grove assumes
// the default location, like it does for ~/.claude and ~/.codex.)
func PiSessionsDir(homeDir, workDir string) string {
	return filepath.Join(homeDir, ".pi", "agent", "sessions", PiSessionDirName(workDir))
}

// PiSessionsGlob returns the glob pattern matching pi session transcript
// files under homeDir, across all per-cwd session subdirectories:
//
//	~/.pi/agent/sessions/--<munged-cwd>--/<timestamp>_<session-uuid>.jsonl
//
// A non-empty sessionID narrows the match to filenames containing that id.
// This is the single definition of the pi session-file layout — discovery
// (pkg/agentstream), scanning (internal/session), and transcript path lookup
// (GetTranscriptPath) all share it.
func PiSessionsGlob(homeDir, sessionID string) string {
	name := "*.jsonl"
	if sessionID != "" {
		name = "*" + sessionID + "*.jsonl"
	}
	return filepath.Join(homeDir, ".pi", "agent", "sessions", "*", name)
}
