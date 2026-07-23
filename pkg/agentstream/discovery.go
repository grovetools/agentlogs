package agentstream

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

// DiscoverOptions configures transcript discovery.
type DiscoverOptions struct {
	Provider   string    // "claude", "codex", "pi", "opencode"
	WorkDir    string    // Working directory to match
	AfterTime  time.Time // Only transcripts modified after this time
	SessionDir string    // Explicit provider session dir (pi); bypasses HOME/cwd derivation
}

// ErrUnsupportedProvider indicates the provider has no file-based transcript
// discovery. It is permanent: callers polling DiscoverTranscript (e.g. waiting
// for a transcript file to appear) should stop retrying when they see it.
var ErrUnsupportedProvider = errors.New("transcript discovery not supported for provider")

// DiscoverTranscript finds the most recent transcript file matching the options.
// For Claude, it looks in ~/.claude/projects/<sanitized-path>/*.jsonl.
// For Codex, it looks in the date-nested ~/.codex/sessions/YYYY/MM/DD/*.jsonl.
// For pi, it looks in the munged-cwd ~/.pi/agent/sessions/--<cwd>--/*.jsonl.
// Opencode is NOT supported: it has no single transcript file to discover —
// it persists fragmented message/part JSON files under
// ~/.local/share/opencode/storage/, keyed by native session ID. Use the
// session-based APIs (internal/provider.OpenCodeSource backed by the opencode
// assembler) instead.
func DiscoverTranscript(opts DiscoverOptions) (string, error) {
	switch opts.Provider {
	case "claude":
		return discoverClaudeTranscript(opts)
	case "codex":
		return discoverCodexTranscript(opts)
	case "pi":
		return discoverPiTranscript(opts)
	case "opencode":
		return "", fmt.Errorf("%w opencode: opencode does not write a single transcript file; it stores fragmented message/part files under ~/.local/share/opencode/storage/ keyed by session ID — use the opencode session APIs (assembler) instead. File-based discovery supports: claude, codex, pi", ErrUnsupportedProvider)
	default:
		return "", fmt.Errorf("%w %s: file-based discovery supports: claude, codex, pi", ErrUnsupportedProvider, opts.Provider)
	}
}

func discoverClaudeTranscript(opts DiscoverOptions) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}

	sanitizedPath := SanitizePathForClaude(opts.WorkDir)
	claudeProjectsDir := filepath.Join(homeDir, ".claude", "projects", sanitizedPath)

	if _, err := os.Stat(claudeProjectsDir); os.IsNotExist(err) {
		return "", fmt.Errorf("Claude projects directory not found: %s", claudeProjectsDir)
	}

	entries, err := os.ReadDir(claudeProjectsDir)
	if err != nil {
		return "", fmt.Errorf("failed to read Claude projects directory: %w", err)
	}

	var latestFile string
	var latestTime time.Time

	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), "agent-") || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		filePath := filepath.Join(claudeProjectsDir, entry.Name())

		contentTime, err := getFirstTimestampFromFile(filePath)
		if err != nil {
			info, infoErr := entry.Info()
			if infoErr != nil {
				continue
			}
			contentTime = info.ModTime()
		}

		if !opts.AfterTime.IsZero() && !contentTime.After(opts.AfterTime) {
			continue
		}

		if contentTime.After(latestTime) {
			latestTime = contentTime
			latestFile = filePath
		}
	}

	if latestFile == "" {
		return "", fmt.Errorf("no Claude session files found in %s", claudeProjectsDir)
	}

	return latestFile, nil
}

func discoverCodexTranscript(opts DiscoverOptions) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}

	codexDir := filepath.Join(homeDir, ".codex", "sessions")
	// Codex nests rollout files by date (YYYY/MM/DD); the shared glob is the
	// single definition of that layout.
	pattern := transcript.CodexSessionsGlob(homeDir, "")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("failed to glob codex sessions: %w", err)
	}

	var latestFile string
	var latestTime time.Time

	for _, filePath := range matches {
		info, err := os.Stat(filePath)
		if err != nil {
			continue
		}
		modTime := info.ModTime()
		if !opts.AfterTime.IsZero() && !modTime.After(opts.AfterTime) {
			continue
		}
		if modTime.After(latestTime) {
			latestTime = modTime
			latestFile = filePath
		}
	}

	if latestFile == "" {
		return "", fmt.Errorf("no Codex session files found in %s", codexDir)
	}

	return latestFile, nil
}

// discoverPiTranscript finds the newest pi session file for a working
// directory. Like Claude, pi maps each cwd to a deterministic per-project
// directory — ~/.pi/agent/sessions/--<cwd-with-separators-as-dashes>--
// (getDefaultSessionDirPath in the pi source's session-manager.ts) — so the
// scan is scoped to that directory. The AfterTime filter uses the session
// header timestamp (first line carries an RFC3339 "timestamp" field) with an
// mtime fallback, mirroring the claude path, so concurrent launches don't
// race for "newest file".
func discoverPiTranscript(opts DiscoverOptions) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}

	piSessionsDir := opts.SessionDir
	if piSessionsDir == "" {
		piSessionsDir = transcript.PiSessionsDir(homeDir, opts.WorkDir)
	}

	if _, err := os.Stat(piSessionsDir); os.IsNotExist(err) {
		return "", fmt.Errorf("pi sessions directory not found: %s", piSessionsDir)
	}

	entries, err := os.ReadDir(piSessionsDir)
	if err != nil {
		return "", fmt.Errorf("failed to read pi sessions directory: %w", err)
	}

	var latestFile string
	var latestTime time.Time

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		filePath := filepath.Join(piSessionsDir, entry.Name())

		contentTime, err := getFirstTimestampFromFile(filePath)
		if err != nil {
			info, infoErr := entry.Info()
			if infoErr != nil {
				continue
			}
			contentTime = info.ModTime()
		}

		if !opts.AfterTime.IsZero() && !contentTime.After(opts.AfterTime) {
			continue
		}

		if contentTime.After(latestTime) {
			latestTime = contentTime
			latestFile = filePath
		}
	}

	if latestFile == "" {
		return "", fmt.Errorf("no pi session files found in %s", piSessionsDir)
	}

	return latestFile, nil
}

// ClaudeTranscriptPath returns the full path to a Claude session's JSONL transcript file.
func ClaudeTranscriptPath(workDir, claudeSessionID string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	sanitizedPath := SanitizePathForClaude(workDir)
	return filepath.Join(homeDir, ".claude", "projects", sanitizedPath, claudeSessionID+".jsonl")
}

// SanitizePathForClaude converts a filesystem path to Claude's project directory name format.
func SanitizePathForClaude(path string) string {
	result := strings.ReplaceAll(path, "/", "-")
	result = strings.ReplaceAll(result, "-.", "--")
	return result
}

// getFirstTimestampFromFile reads the first timestamp from a JSONL file.
// More reliable than file modification time on some filesystems.
func getFirstTimestampFromFile(filePath string) (time.Time, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return time.Time{}, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for i := 0; i < 10 && scanner.Scan(); i++ {
		var entry struct {
			Timestamp time.Time `json:"timestamp"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err == nil && !entry.Timestamp.IsZero() {
			return entry.Timestamp, nil
		}
	}

	return time.Time{}, fmt.Errorf("no timestamp found in first 10 lines of %s", filePath)
}
