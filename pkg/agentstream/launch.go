package agentstream

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/pkg/process"
)

const (
	pidPollInterval = 25 * time.Millisecond
	pidWaitTimeout  = 30 * time.Second
	ackGracePeriod  = 250 * time.Millisecond
)

// deadPIDGrace gives a new launch time to replace a stale, dead PID while still
// allowing a provider that exits immediately to be discovered and reported.
var deadPIDGrace = 3 * time.Second

// SupervisorOptions describes one interactive provider invocation.
// AgentCommand and ReporterCommand are opaque shell command strings. The
// supervisor appends the provider's numeric exit code to ReporterCommand.
type SupervisorOptions struct {
	PIDFile         string
	AgentCommand    string
	ReporterCommand string
}

// BuildSupervisedAgentCommand builds the pane command used to enter the Go
// supervisor. supervisorCommand is a trusted command prefix supplied by the
// embedding executable (for example, "flow agent supervise").
//
// The returned command removes receipts from an older attempt before execing
// the supervisor. Call PreparePIDFile immediately before dispatch as well, so
// WaitForPID cannot observe a stale receipt while the pane command is queued.
func BuildSupervisedAgentCommand(jobID, supervisorCommand, agentCommand, reporterCommand string) string {
	pidFile := PidFilePath(jobID)
	// Best-effort preparation closes the window before the pane command runs;
	// the returned command repeats it to cover a receipt created while queued.
	_ = PreparePIDFile(jobID)
	return fmt.Sprintf(
		"rm -f %s %s && exec %s --pid-file %s --agent-command %s --reporter-command %s",
		shellSingleQuote(pidFile),
		shellSingleQuote(pidAckPath(pidFile)),
		supervisorCommand,
		shellSingleQuote(pidFile),
		shellSingleQuote(agentCommand),
		shellSingleQuote(reporterCommand),
	)
}

// BuildAgentCommand retains the legacy unsupervised wrapper for callers which
// have not yet adopted BuildSupervisedAgentCommand.
func BuildAgentCommand(jobID, agentCmd string) string {
	pidFile := PidFilePath(jobID)
	// Legacy callers have no supervisor-side receipt cleanup, so command
	// construction remains their launch preparation choke point.
	_ = PreparePIDFile(jobID)
	script := fmt.Sprintf("mkdir -p %s && echo $$ > %s && exec %s",
		shellSingleQuote(filepath.Dir(pidFile)),
		shellSingleQuote(pidFile),
		agentCmd,
	)
	return "sh -c " + shellSingleQuote(script)
}

// RunSupervisor launches the provider with inherited stdio, records the
// provider PID, forwards SIGTERM/SIGINT, waits for it, invokes the reporter,
// and returns the provider's shell-compatible exit code.
//
// ReporterCommand is a command prefix: the decimal exit code is appended as
// its final shell argument. Reporter failure is diagnosed on stderr but never
// replaces the provider's exit code.
func RunSupervisor(ctx context.Context, opts SupervisorOptions) int {
	if opts.PIDFile == "" || opts.AgentCommand == "" || opts.ReporterCommand == "" {
		fmt.Fprintln(os.Stderr, "agent supervisor: pid file, agent command, and reporter command are required")
		return 2
	}

	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)

	provider := exec.Command("sh", "-c", "exec "+opts.AgentCommand)
	provider.Stdin = os.Stdin
	provider.Stdout = os.Stdout
	provider.Stderr = os.Stderr
	if err := provider.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "agent supervisor: start provider: %v\n", err)
		runReporter(opts.ReporterCommand, 127)
		return 127
	}

	pid := provider.Process.Pid
	// Capture the process group while the provider is alive. Once Wait returns,
	// Getpgid(provider.Pid) can no longer recover it, but surviving tool children
	// remain in that group and must not be orphaned.
	providerGroup, _ := syscall.Getpgid(pid)
	if err := writePIDReceipt(opts.PIDFile, pid); err != nil {
		fmt.Fprintf(os.Stderr, "agent supervisor: write pid receipt: %v\n", err)
		_ = provider.Process.Kill()
		_ = provider.Wait()
		runReporter(opts.ReporterCommand, 1)
		return 1
	}

	done := make(chan struct{})
	go func() {
		select {
		case sig := <-signals:
			forwardSignal(provider.Process, sig)
		case <-ctx.Done():
			forwardSignal(provider.Process, syscall.SIGTERM)
		case <-done:
		}
	}()

	err := provider.Wait()
	close(done)
	rc := processExitCode(err)
	terminateRemainingProviderGroup(providerGroup)
	runReporter(opts.ReporterCommand, rc)
	cleanupAcknowledgedReceipt(opts.PIDFile, pid)
	return rc
}

// terminateRemainingProviderGroup cleans up tool subprocesses left behind when
// the provider exits or is killed. In an interactive pane the supervisor is
// normally the foreground process-group leader and the provider inherits that
// group; temporarily ignoring TERM lets the supervisor signal the rest of its
// own group without terminating before it can report the provider outcome.
// In non-interactive embedding, an inherited group may also contain the caller,
// so that unsafe shape is deliberately left alone.
func terminateRemainingProviderGroup(providerGroup int) {
	if providerGroup <= 0 {
		return
	}
	selfGroup := syscall.Getpgrp()
	switch {
	case providerGroup != selfGroup:
		_ = syscall.Kill(-providerGroup, syscall.SIGTERM)
	case selfGroup == os.Getpid():
		signal.Ignore(syscall.SIGTERM)
		_ = syscall.Kill(-selfGroup, syscall.SIGTERM)
		signal.Reset(syscall.SIGTERM)
	}
}

func runReporter(command string, rc int) {
	// Reporting is part of supervision, not part of the provider's cancellable
	// lifetime. A cancelled launch context must not suppress the exit report.
	reporter := exec.Command("sh", "-c", "exec "+command+" "+strconv.Itoa(rc))
	reporter.Stdin = os.Stdin
	reporter.Stdout = os.Stdout
	reporter.Stderr = os.Stderr
	if err := reporter.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "agent supervisor: reporter failed: %v\n", err)
	}
}

func processExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal())
		}
		if code := exitErr.ExitCode(); code >= 0 {
			return code
		}
	}
	return 1
}

func forwardSignal(process *os.Process, sig os.Signal) {
	sysSig, ok := sig.(syscall.Signal)
	if !ok {
		_ = process.Signal(sig)
		return
	}

	childGroup, childErr := syscall.Getpgid(process.Pid)
	selfGroup := syscall.Getpgrp()
	switch {
	case childErr == nil && childGroup != selfGroup:
		_ = syscall.Kill(-childGroup, sysSig)
	case selfGroup == os.Getpid():
		// Interactive shells normally make the supervisor the foreground group
		// leader. Ignore this forwarded signal in the supervisor itself, then
		// deliver it to the whole group so provider subprocesses receive it.
		signal.Ignore(sysSig)
		_ = syscall.Kill(-selfGroup, sysSig)
	default:
		// Do not signal an inherited non-interactive process group (which may
		// contain the caller or test harness).
		_ = process.Signal(sysSig)
	}
}

// PreparePIDFile removes receipts from a prior attempt. Call it immediately
// before dispatching the supervisor command.
func PreparePIDFile(jobID string) error {
	return removePIDReceipts(PidFilePath(jobID))
}

// WaitForPID watches for the pidfile and returns the provider PID. Before it
// returns, it writes an acknowledgement. The supervisor keeps the pidfile
// through reporter execution and removes it only after observing that ack;
// therefore instant provider exits cannot race PID discovery.
func WaitForPID(ctx context.Context, jobID string) (int, error) {
	pidFile := PidFilePath(jobID)
	ticker := time.NewTicker(pidPollInterval)
	defer ticker.Stop()
	timer := time.NewTimer(pidWaitTimeout)
	defer timer.Stop()

	var deadPID int
	var deadSince time.Time
	acknowledge := func(pid int) (int, error) {
		if err := writePIDReceipt(pidAckPath(pidFile), pid); err != nil {
			return 0, fmt.Errorf("acknowledge pidfile %s: %w", pidFile, err)
		}
		return pid, nil
	}

	for {
		if pid, ok := readPID(pidFile); ok {
			if process.IsProcessAlive(pid) {
				return acknowledge(pid)
			}
			if pid != deadPID {
				deadPID, deadSince = pid, time.Now()
			} else if time.Since(deadSince) >= deadPIDGrace {
				return acknowledge(deadPID)
			}
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-timer.C:
			if deadPID > 0 {
				return acknowledge(deadPID)
			}
			return 0, fmt.Errorf("timeout waiting for pidfile %s", pidFile)
		case <-ticker.C:
		}
	}
}

// CleanupPIDFile removes the pidfile and acknowledgement. If expectedPID is
// supplied, cleanup is compare-and-delete so an older discovery goroutine
// cannot remove a relaunch's receipt. The variadic form retains compatibility
// with supervised callers, whose acknowledgement already identifies the PID.
func CleanupPIDFile(jobID string, expectedPID ...int) error {
	pidFile := PidFilePath(jobID)
	if len(expectedPID) > 0 {
		pid, ok := readPID(pidFile)
		if !ok {
			return nil
		}
		if pid != expectedPID[0] {
			return nil
		}
	}
	return removePIDReceipts(pidFile)
}

// PidFilePath returns the deterministic path for a job's pidfile.
func PidFilePath(jobID string) string {
	return filepath.Join(paths.RuntimeDir(), fmt.Sprintf("grove-agent-%s.pid", jobID))
}

func readPID(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid, err == nil && pid > 0
}

func writePIDReceipt(path string, pid int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pid-receipt-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := fmt.Fprintf(tmp, "%d\n", pid); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func cleanupAcknowledgedReceipt(pidFile string, pid int) {
	deadline := time.Now().Add(ackGracePeriod)
	for {
		if ackPID, ok := readPID(pidAckPath(pidFile)); ok && ackPID == pid {
			_ = removePIDReceipts(pidFile)
			return
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func removePIDReceipts(pidFile string) error {
	var firstErr error
	for _, path := range []string{pidFile, pidAckPath(pidFile)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func pidAckPath(pidFile string) string {
	return pidFile + ".ack"
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
