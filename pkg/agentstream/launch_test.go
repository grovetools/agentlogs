package agentstream

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/grovetools/core/pkg/process"
)

// isolateRuntimeDir points paths.RuntimeDir() at a throwaway directory so a test
// can never read or unlink a real agent's pidfile.
func isolateRuntimeDir(t *testing.T) {
	t.Helper()
	t.Setenv("GROVE_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(PidFilePath("probe")), 0o755); err != nil {
		t.Fatalf("mkdir runtime dir: %v", err)
	}
}

// reapedPID returns a PID that is certainly not running: a child we started and
// waited on, so the kernel has released it.
func reapedPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start throwaway child: %v", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Wait()
	if process.IsProcessAlive(pid) {
		t.Fatalf("pid %d still alive after Wait; cannot build a dead-pid fixture", pid)
	}
	return pid
}

func writePIDFile(t *testing.T, jobID string, pid int) {
	t.Helper()
	if err := os.WriteFile(PidFilePath(jobID), []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		t.Fatalf("write pidfile: %v", err)
	}
}

// TestBuildAgentCommandClearsAPreviousLaunchesPIDFile is the steward-66dd4eb3
// regression (2026-08-01). The pidfile path is deterministic per job ID, so a
// launch whose discovery goroutine died before CleanupPIDFile ran left pid 12072
// on disk; the next launch's WaitForPID read that corpse on its first tick and
// handed it to the daemon, which can never reap a session whose PID was already
// dead at confirmation.
//
// Preparing the command is the launch step, so the leftover must be gone by the
// time it returns — before the agent's shell has had any chance to race it.
func TestBuildAgentCommandClearsAPreviousLaunchesPIDFile(t *testing.T) {
	isolateRuntimeDir(t)
	const jobID = "steward-stale"
	stale := reapedPID(t)
	writePIDFile(t, jobID, stale)

	cmd := BuildAgentCommand(jobID, "grove-agent --resume native-1")

	if _, err := os.Stat(PidFilePath(jobID)); !os.IsNotExist(err) {
		data, _ := os.ReadFile(PidFilePath(jobID))
		t.Fatalf("pidfile survived launch preparation (contents %q, stat err %v); WaitForPID can still return the previous launch's pid %d", strings.TrimSpace(string(data)), err, stale)
	}
	if !strings.Contains(cmd, PidFilePath(jobID)) {
		t.Fatalf("wrapped command lost the pidfile path: %s", cmd)
	}
}

// TestWaitForPIDIgnoresAStaleFileFromABeforeTheLaunch is the same regression
// driven end to end, and it fails on the unlink alone: the fresh write lands
// after deadPIDGrace has expired, so the stale corpse would be returned if the
// launch had not removed the file first.
func TestWaitForPIDIgnoresAStaleFileFromABeforeTheLaunch(t *testing.T) {
	isolateRuntimeDir(t)
	deadPIDGrace = 150 * time.Millisecond
	t.Cleanup(func() { deadPIDGrace = 3 * time.Second })

	const jobID = "steward-stale-e2e"
	stale := reapedPID(t)
	writePIDFile(t, jobID, stale)

	_ = BuildAgentCommand(jobID, "grove-agent")

	// Stand in for the agent's shell reaching `echo $$`, slowly enough that a
	// surviving stale file would already have been reported.
	written := make(chan struct{})
	go func() {
		defer close(written)
		time.Sleep(400 * time.Millisecond)
		_ = os.WriteFile(PidFilePath(jobID), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)
	}()
	t.Cleanup(func() { <-written })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pid, err := WaitForPID(ctx, jobID)
	if err != nil {
		t.Fatalf("WaitForPID: %v", err)
	}
	if pid == stale {
		t.Fatalf("WaitForPID returned the previous launch's dead pid %d; the daemon would confirm a session it can never reap", stale)
	}
	if pid != os.Getpid() {
		t.Fatalf("WaitForPID = %d, want the launching shell's pid %d", pid, os.Getpid())
	}
}

// TestWaitForPIDPrefersALivePIDOverACorpse pins the second line of defence: even
// if the stale file survives (unremovable runtime dir, or two launches of one
// job racing), a PID that is already dead must not beat the write that follows
// it.
func TestWaitForPIDPrefersALivePIDOverACorpse(t *testing.T) {
	isolateRuntimeDir(t)
	deadPIDGrace = 2 * time.Second
	t.Cleanup(func() { deadPIDGrace = 3 * time.Second })

	const jobID = "steward-corpse"
	stale := reapedPID(t)
	writePIDFile(t, jobID, stale)

	written := make(chan struct{})
	go func() {
		defer close(written)
		time.Sleep(300 * time.Millisecond)
		_ = os.WriteFile(PidFilePath(jobID), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)
	}()
	t.Cleanup(func() { <-written })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pid, err := WaitForPID(ctx, jobID)
	if err != nil {
		t.Fatalf("WaitForPID: %v", err)
	}
	if pid != os.Getpid() {
		t.Fatalf("WaitForPID = %d, want the live pid %d (a dead pid must not win the race)", pid, os.Getpid())
	}
}

// TestWaitForPIDStillReportsADeadPIDAfterTheGrace protects the Pi
// startup-failure path: when the agent really did exit seconds after spawn, its
// dead PID is the evidence handlePiStartupFailure uses to fail the job instead
// of leaving it "running" forever. Deprioritised, never discarded.
func TestWaitForPIDStillReportsADeadPIDAfterTheGrace(t *testing.T) {
	isolateRuntimeDir(t)
	deadPIDGrace = 150 * time.Millisecond
	t.Cleanup(func() { deadPIDGrace = 3 * time.Second })

	const jobID = "pi-crashed"
	crashed := reapedPID(t)
	writePIDFile(t, jobID, crashed)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	pid, err := WaitForPID(ctx, jobID)
	if err != nil {
		t.Fatalf("WaitForPID: %v", err)
	}
	if pid != crashed {
		t.Fatalf("WaitForPID = %d, want the crashed agent's pid %d", pid, crashed)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("WaitForPID took %s to report a dead pid; startup-failure diagnosis must not wait out the full timeout", elapsed)
	}
}

// TestCleanupPIDFileOnlyRemovesTheCallersOwnPID covers the tail of a long
// discovery goroutine: by the time it cleans up, a relaunch of the same job may
// already own the pidfile, and unlinking that would strand the new launch's
// WaitForPID on a path nothing will write again.
func TestCleanupPIDFileOnlyRemovesTheCallersOwnPID(t *testing.T) {
	isolateRuntimeDir(t)
	const jobID = "steward-cleanup"

	t.Run("removes the pid it was handed", func(t *testing.T) {
		writePIDFile(t, jobID, 4242)
		if err := CleanupPIDFile(jobID, 4242); err != nil {
			t.Fatalf("CleanupPIDFile: %v", err)
		}
		if _, err := os.Stat(PidFilePath(jobID)); !os.IsNotExist(err) {
			t.Fatalf("pidfile still present after cleanup: %v", err)
		}
	})

	t.Run("leaves a relaunch's pid alone", func(t *testing.T) {
		writePIDFile(t, jobID, 5150)
		if err := CleanupPIDFile(jobID, 4242); err != nil {
			t.Fatalf("CleanupPIDFile: %v", err)
		}
		data, err := os.ReadFile(PidFilePath(jobID))
		if err != nil {
			t.Fatalf("the newer launch's pidfile was deleted: %v", err)
		}
		if got := strings.TrimSpace(string(data)); got != "5150" {
			t.Fatalf("pidfile contents = %q, want the newer launch's 5150", got)
		}
	})

	t.Run("a missing pidfile is not an error", func(t *testing.T) {
		_ = os.Remove(PidFilePath(jobID))
		if err := CleanupPIDFile(jobID, 4242); err != nil {
			t.Fatalf("CleanupPIDFile on a missing file = %v, want nil", err)
		}
	})
}
