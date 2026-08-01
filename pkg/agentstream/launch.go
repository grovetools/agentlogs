package agentstream

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/pkg/process"
)

// deadPIDGrace bounds how long WaitForPID keeps polling after reading a pidfile
// whose PID is already dead, hoping the launch's own shell overwrites it.
//
// This is defence in depth behind clearStalePIDFile: if a leftover file somehow
// survives the launch (an unremovable runtime dir, or two launches of one job
// racing), the window in which a corpse can be reported as the agent shrinks
// from "forever" to this. In the 2026-08-01 incident the real shell rewrote the
// file within the same second the wait started, so a few seconds is ample for
// the truthful write to win.
//
// It deliberately does not reject dead PIDs outright. A Pi that fails to load an
// extension exits about a second after spawn, and its real (now dead) PID is the
// evidence handlePiStartupFailure uses to mark the job failed instead of leaving
// it "running" forever; requiring liveness would discard that and turn a fast
// crash into a 30s timeout. A dead PID is deprioritised, never discarded.
//
// The other candidate — ignoring a pidfile whose mtime predates the wait — was
// rejected: the wrapper legitimately writes the file *before* WaitForPID is
// entered (every provider sends the command, then does daemon RPCs, and only
// then starts discovery), so "older than the wait" describes healthy launches
// too. Sub-second mtime discrimination is not portable enough to fix that.
//
// A var, not a const, only so tests can collapse it; nothing mutates it at
// runtime.
var deadPIDGrace = 3 * time.Second

// BuildAgentCommand wraps an agent command to capture its PID deterministically.
// The returned command writes the shell PID to a pidfile before exec'ing the agent binary,
// so the caller can simply watch for the file rather than traversing process trees.
//
// The command is wrapped in `sh -c '...'` to ensure POSIX $$ works regardless of the
// user's login shell (fish uses $fish_pid instead of $$, for example).
//
// Building the command is also the launch's preparation step: it clears any
// pidfile a previous launch of this job left behind (see clearStalePIDFile), so
// the only file WaitForPID can possibly observe is the one this launch writes.
//
// Example: for "claude --model opus", returns:
//
//	sh -c 'mkdir -p /path/to && echo $$ > /path/to/grove-agent-<jobID>.pid && exec claude --model opus'
func BuildAgentCommand(jobID, agentCmd string) string {
	pidFile := PidFilePath(jobID)
	clearStalePIDFile(jobID)
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

// clearStalePIDFile removes a previous launch's pidfile so WaitForPID cannot
// observe a file that predates the launch it is waiting on.
//
// PidFilePath is deterministic per job ID, so every launch of a job reuses the
// identical path, and the wrapper's truncating `echo $$ >` only lands once the
// agent's shell actually runs. Observed 2026-08-01 on job steward-66dd4eb3: a
// launch at 17:26 lost its discovery goroutine to process exit before
// CleanupPIDFile could run, leaving the file holding pid 12072; that process
// died. The relaunch at 18:30 spawned a healthy agent (pid 40179), but WaitForPID
// read the leftover on its first 100ms tick and returned 12072, beating the new
// shell's `echo $$` — "Discovered agent PID via pidfile pid=12072" at 18:30:36,
// "Confirmed groveterm agent session pid=12072" at 18:30:42, while the file on
// disk already read 40179.
//
// The damage outlives the wrong number: the daemon's session collector only
// reaps a PID it has positively observed alive (`if !ls.seenAlive { continue }`),
// and a PID that was already dead at confirmation can never flip that flag, so
// the session becomes permanently unreapable and lingers idle after the real
// agent exits — with the assistant supervisor reading that record as proof a
// live session is present. For Pi providers a stale corpse is worse still: it
// reads as `processExited` to handlePiStartupFailure, which can fail a job whose
// agent is merely slow to write its first transcript.
//
// This lives at command-build time rather than in each provider because every
// launch path — groveterm, claude, codex, pi, and the isolated executor — must
// go through BuildAgentCommand to get a command that writes a pidfile at all. A
// provider added later cannot forget it.
//
// Removal is best-effort: a runtime dir we cannot unlink in must not block a
// launch (the agent matters more than the bookkeeping), and deadPIDGrace is the
// second line of defence for exactly that case.
func clearStalePIDFile(jobID string) {
	_ = os.Remove(PidFilePath(jobID))
}

// WaitForPID watches for the pidfile and returns the agent's PID.
// Blocks until a usable PID appears, ctx is cancelled, or timeout (30s) is reached.
//
// A PID that is already dead when read is held as a fallback rather than
// returned straight away — see deadPIDGrace for why it is neither trusted
// immediately nor rejected.
func WaitForPID(ctx context.Context, jobID string) (int, error) {
	pidFile := PidFilePath(jobID)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(30 * time.Second)

	// The most recent dead PID read out of the pidfile, and when it first
	// appeared. Reset whenever a different PID shows up, so a rewrite gets its
	// own grace rather than inheriting an expired one.
	var deadPID int
	var deadSince time.Time

	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-timeout:
			if deadPID > 0 {
				return deadPID, nil
			}
			return 0, fmt.Errorf("timeout waiting for pidfile %s", pidFile)
		case <-ticker.C:
			data, err := os.ReadFile(pidFile)
			if err != nil {
				continue
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil || pid <= 0 {
				continue
			}
			if process.IsProcessAlive(pid) {
				return pid, nil
			}
			if pid != deadPID {
				deadPID, deadSince = pid, time.Now()
				continue
			}
			if time.Since(deadSince) >= deadPIDGrace {
				return deadPID, nil
			}
		}
	}
}

// CleanupPIDFile removes the pidfile for the given job, but only while it still
// holds the PID the caller consumed.
//
// The compare guards a race a bare unlink cannot see. The path is deterministic
// per job ID, and callers reach cleanup at the end of a discovery goroutine that
// can run for tens of seconds (a 30s PID wait plus ten one-second transcript
// retries), by which point a relaunch of the same job may already have written
// its own PID here. Removing that file would strand the new launch's WaitForPID
// on a path nothing will ever write again. A mismatch means the file is not ours,
// so we leave it: BuildAgentCommand clears whatever is there at the next launch,
// which is the one moment deleting is unambiguously safe. That is also why the
// failure paths deliberately do not force a cleanup — a leaked pidfile can no
// longer poison a later launch, and an eager unlink there would be the delete
// most likely to hit a concurrent launch's file.
//
// A missing file is not an error; it is the expected state after a launch whose
// agent never ran.
func CleanupPIDFile(jobID string, pid int) error {
	path := PidFilePath(jobID)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	current, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || current != pid {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// PidFilePath returns the deterministic path for a job's pidfile.
func PidFilePath(jobID string) string {
	return filepath.Join(paths.RuntimeDir(), fmt.Sprintf("grove-agent-%s.pid", jobID))
}
