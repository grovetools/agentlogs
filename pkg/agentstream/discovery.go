package agentstream

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DiscoverOptions configures transcript discovery.
type DiscoverOptions struct {
	Provider  string    // "claude", "codex", "opencode"
	WorkDir   string    // Working directory to match
	AfterTime time.Time // Only transcripts modified after this time
}

// DiscoverTranscript finds the most recent transcript file matching the options.
// For Claude, it looks in ~/.claude/projects/<sanitized-path>/*.jsonl.
// For Codex, it looks in ~/.codex/sessions/*.jsonl.
func DiscoverTranscript(opts DiscoverOptions) (string, error) {
	switch opts.Provider {
	case "claude":
		return discoverClaudeTranscript(opts)
	case "codex":
		return discoverCodexTranscript(opts)
	default:
		return "", fmt.Errorf("unsupported provider for transcript discovery: %s", opts.Provider)
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
	pattern := filepath.Join(codexDir, "*.jsonl")
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
