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
	return PiSessionsDirForConfig(homeDir, ".pi", workDir)
}

// PiSessionsDirForConfig returns the session root for one Pi-family product.
// Rebranded runtimes retain Pi's layout under their own config directory.
func PiSessionsDirForConfig(homeDir, configDirName, workDir string) string {
	return filepath.Join(homeDir, configDirName, "agent", "sessions", PiSessionDirName(workDir))
}

// IsPiSessionPath reports whether a filesystem path looks like a pi session
// transcript file.
//
// It recognizes the layout STRUCTURALLY rather than by matching a fixed prefix,
// because the whole ~/.pi/agent prefix is overridable via PI_CODING_AGENT_DIR:
// a pi session file is a .jsonl whose parent directory is a munged-cwd
// directory ("--Users-foo-bar--", see PiSessionDirName) sitting under a
// directory named "sessions". That shape survives the env override, and it also
// matches the committed testdata layout, whereas any absolute-prefix match
// would not.
//
// The "/.pi/" fallback mirrors providerFromTranscriptPath in
// internal/session/scanner.go, which is the repo's existing (and correct)
// spelling of this inference.
//
// Note the substring "/pi/sessions/" — used by an earlier version of
// cmd/metrics.go — never occurs in a real pi path, because the real layout is
// ~/.pi/agent/sessions/. Matching it silently resolved every pi transcript as
// claude.
func IsPiSessionPath(path string) bool {
	slashed := filepath.ToSlash(path)
	if strings.Contains(slashed, "/.pi/") || strings.Contains(slashed, "/.grove-agent/") {
		return true
	}
	if strings.ToLower(filepath.Ext(path)) != ".jsonl" {
		return false
	}
	parent := filepath.Base(filepath.Dir(path))
	grandparent := filepath.Base(filepath.Dir(filepath.Dir(path)))
	return grandparent == PiSessionsDirName &&
		len(parent) >= 4 && strings.HasPrefix(parent, "--") && strings.HasSuffix(parent, "--")
}

// PiSessionsDirName is the directory segment pi stores per-cwd session
// directories under: ~/.pi/agent/<PiSessionsDirName>/--<munged-cwd>--/.
const PiSessionsDirName = "sessions"

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
	return PiSessionsGlobForConfig(homeDir, ".pi", sessionID)
}

// PiSessionsGlobForConfig keeps stock Pi and rebranded product stores
// independently discoverable.
func PiSessionsGlobForConfig(homeDir, configDirName, sessionID string) string {
	name := "*.jsonl"
	if sessionID != "" {
		name = "*" + sessionID + "*.jsonl"
	}
	return filepath.Join(homeDir, configDirName, "agent", "sessions", "*", name)
}
