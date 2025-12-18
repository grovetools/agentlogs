package session

import "time"

// JobInfo holds information about a grove plan job found in the transcript
type JobInfo struct {
	Plan      string `json:"plan"`
	Job       string `json:"job"`
	LineIndex int    `json:"lineIndex"`
}

// SessionInfo holds structured information about a session transcript
type SessionInfo struct {
	SessionID   string    `json:"sessionId"`
	ProjectName string    `json:"projectName"`
	ProjectPath string    `json:"projectPath"`
	Worktree    string    `json:"worktree,omitempty"`
	Ecosystem   string    `json:"ecosystem,omitempty"`
	Jobs        []JobInfo `json:"jobs,omitempty"`
	LogFilePath string    `json:"logFilePath"`
	StartedAt   time.Time `json:"startedAt"`
	Provider    string    `json:"provider,omitempty"` // "claude" or "codex"
}
