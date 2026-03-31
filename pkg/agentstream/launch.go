package agentstream

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/grovetools/core/pkg/paths"
)

// BuildAgentCommand wraps an agent command to capture its PID deterministically.
// The returned command writes the shell PID to a pidfile before exec'ing the agent binary,
// so the caller can simply watch for the file rather than traversing process trees.
//
// The command is wrapped in `sh -c '...'` to ensure POSIX $$ works regardless of the
// user's login shell (fish uses $fish_pid instead of $$, for example).
//
// Example: for "claude --model opus", returns:
//
//	sh -c 'mkdir -p /path/to && echo $$ > /path/to/grove-agent-<jobID>.pid && exec claude --model opus'
func BuildAgentCommand(jobID string, agentCmd string) string {
	pidFile := PidFilePath(jobID)
	// Escape single quotes in the agent command for embedding in sh -c '...'
	escapedCmd := strings.ReplaceAll(agentCmd, "'", "'\"'\"'")
	escapedDir := strings.ReplaceAll(filepath.Dir(pidFile), "'", "'\"'\"'")
	escapedPidFile := strings.ReplaceAll(pidFile, "'", "'\"'\"'")
	return fmt.Sprintf("sh -c 'mkdir -p %s && echo $$ > %s && exec %s'",
		escapedDir,
		escapedPidFile,
		escapedCmd,
	)
}

// WaitForPID watches for the pidfile and returns the agent's PID.
// Blocks until the file appears, ctx is cancelled, or timeout (30s) is reached.
func WaitForPID(ctx context.Context, jobID string) (int, error) {
	pidFile := PidFilePath(jobID)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(30 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-timeout:
			return 0, fmt.Errorf("timeout waiting for pidfile %s", pidFile)
		case <-ticker.C:
			data, err := os.ReadFile(pidFile)
			if err != nil {
				continue
			}
			pidStr := strings.TrimSpace(string(data))
			if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
				return pid, nil
			}
		}
	}
}

// CleanupPIDFile removes the pidfile for the given job.
func CleanupPIDFile(jobID string) error {
	return os.Remove(PidFilePath(jobID))
}

// PidFilePath returns the deterministic path for a job's pidfile.
func PidFilePath(jobID string) string {
	return filepath.Join(paths.RuntimeDir(), fmt.Sprintf("grove-agent-%s.pid", jobID))
}

