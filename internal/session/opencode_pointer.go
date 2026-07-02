package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/grovetools/core/pkg/paths"
)

// opencodePointerMetadata is the subset of the grove-hooks session registry
// metadata that the grove opencode plugin (grove-integration.ts v2) records:
// the standard fields the Go pipeline writes plus the opencode transcript
// pointer (native_session_id + opencode_storage_root). The pointer lets the
// resolver map a spec to the session's fragment store directly instead of
// scanning all of opencode storage.
type opencodePointerMetadata struct {
	SessionID        string    `json:"session_id"`
	ClaudeSessionID  string    `json:"claude_session_id"`
	Provider         string    `json:"provider"`
	NativeSessionID  string    `json:"native_session_id"`
	StorageRoot      string    `json:"opencode_storage_root"`
	WorkingDirectory string    `json:"working_directory"`
	PlanName         string    `json:"plan_name"`
	JobFilePath      string    `json:"job_file_path"`
	Status           string    `json:"status"`
	PID              int       `json:"pid"`
	StartedAt        time.Time `json:"started_at"`
}

// nativeID returns the opencode session id, preferring the explicit pointer
// field over the legacy claude_session_id slot (pre-pointer plugin installs).
func (m opencodePointerMetadata) nativeID() string {
	if m.NativeSessionID != "" {
		return m.NativeSessionID
	}
	return m.ClaudeSessionID
}

// resolveOpenCodePointer resolves spec (a flow job id, a native ses_* id, a
// registry directory name, or a plan/job pair) against the grove-hooks
// session registry and, for opencode sessions, follows the recorded
// transcript pointer to the session info file inside opencode's fragment
// store. Returns nil when no registry entry matches — callers fall back to
// the full scan.
func resolveOpenCodePointer(spec string) *SessionInfo {
	if spec == "" {
		return nil
	}

	sessionsDir := filepath.Join(paths.StateDir(), "hooks", "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil
	}

	parts := strings.Split(spec, "/")
	isPlanJobSpec := len(parts) == 2 && strings.HasSuffix(parts[1], ".md")

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(sessionsDir, entry.Name(), "metadata.json"))
		if err != nil {
			continue
		}
		var m opencodePointerMetadata
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		if m.Provider != "opencode" {
			continue
		}
		native := m.nativeID()
		if native == "" {
			continue
		}

		matched := spec == m.SessionID || spec == native || spec == entry.Name()
		if !matched && isPlanJobSpec {
			matched = m.PlanName == parts[0] && m.JobFilePath != "" && filepath.Base(m.JobFilePath) == parts[1]
		}
		if !matched {
			continue
		}

		logPath := openCodeSessionInfoPath(m.StorageRoot, native)
		if logPath == "" {
			// Pointer recorded but the fragment store has no session info
			// file (yet) — let the caller's fallback tiers handle it.
			continue
		}

		var jobs []JobInfo
		if m.PlanName != "" && m.JobFilePath != "" {
			jobs = append(jobs, JobInfo{
				Plan: m.PlanName,
				Job:  filepath.Base(m.JobFilePath),
			})
		}

		scanner := NewScannerWithoutDaemon()
		projectPath, projectName, worktree, ecosystem := scanner.parseProjectPath(m.WorkingDirectory)

		return &SessionInfo{
			SessionID:   native,
			ProjectName: projectName,
			ProjectPath: projectPath,
			Worktree:    worktree,
			Ecosystem:   ecosystem,
			Jobs:        jobs,
			LogFilePath: logPath,
			StartedAt:   m.StartedAt,
			Provider:    "opencode",
			Status:      m.Status,
			PID:         m.PID,
		}
	}
	return nil
}

// openCodeSessionInfoPath locates the session info file
// (<storage>/session/<projectID>/<nativeID>.json) for a native opencode
// session id. The project id is unknown to the registry, so a single-level
// glob resolves it. An empty storageRoot falls back to opencode's default
// XDG data location.
func openCodeSessionInfoPath(storageRoot, nativeID string) string {
	if nativeID == "" {
		return ""
	}
	if storageRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		storageRoot = filepath.Join(home, ".local", "share", "opencode", "storage")
	}
	matches, err := filepath.Glob(filepath.Join(storageRoot, "session", "*", nativeID+".json"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	return matches[0]
}
