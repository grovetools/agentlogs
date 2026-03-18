package provider

import (
	"context"

	"github.com/grovetools/agentlogs/internal/session"
	"github.com/grovetools/core/pkg/daemon"
)

// SelectSource determines the best TranscriptSource for a given session.
// If the daemon is running and manages this job, it returns a DaemonSource.
// Otherwise, it falls back to a direct file-based provider.
func SelectSource(info *session.SessionInfo, daemonClient daemon.Client) TranscriptSource {
	if daemonClient != nil && info.SessionID != "" && info.SessionID != "unknown" {
		if daemonClient.IsRunning() {
			if job, _ := daemonClient.GetJob(context.Background(), info.SessionID); job != nil {
				if job.Type == "interactive_agent" || job.Type == "headless_agent" || job.Type == "isolated_agent" {
					// Agent jobs run in tmux — the daemon SSE stream has no transcript content.
					// Fall through to file-based source to read the actual transcript.
				} else {
					return NewDaemonSource(daemonClient, info)
				}
			}
		}
	}

	switch info.Provider {
	case "opencode":
		return NewOpenCodeSource()
	case "codex":
		return NewCodexSource()
	default:
		return NewClaudeSource()
	}
}
