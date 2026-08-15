package agentstream

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/grovetools/core/pkg/process"
)

const helperEnv = "AGENTSTREAM_LAUNCH_HELPER"

func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "supervisor":
		os.Exit(RunSupervisor(context.Background(), SupervisorOptions{
			PIDFile:         os.Getenv("HELPER_PID_FILE"),
			AgentCommand:    os.Getenv("HELPER_AGENT_COMMAND"),
			ReporterCommand: os.Getenv("HELPER_REPORTER_COMMAND"),
		}))
	case "provider":
		runProviderHelper()
	case "reporter":
		runReporterHelper()
	}
	os.Exit(m.Run())
}

func TestRunSupervisorProviderPIDAndExitCode(t *testing.T) {
	files := newLaunchFiles(t)
	cmd := supervisorProcess(t, files, providerCommand(t, files, "exit", 37))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	wrapperPID := cmd.Process.Pid
	providerPID := waitForPIDPath(t, files.pid)
	if providerPID == wrapperPID {
		t.Fatalf("pidfile contains supervisor PID %d", wrapperPID)
	}
	providerData, err := waitForFile(files.provider, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if recorded := strings.TrimSpace(string(providerData)); recorded != strconv.Itoa(providerPID) {
		t.Fatalf("pidfile PID %d differs from provider's own PID %s", providerPID, recorded)
	}
	if err := cmd.Wait(); exitCode(err) != 37 {
		t.Fatalf("supervisor exit = %d, want 37 (%v)", exitCode(err), err)
	}
	assertReporter(t, files, 37, 1, true)
}

func TestRunSupervisorTERMProvider(t *testing.T) {
	files := newLaunchFiles(t)
	cmd := supervisorProcess(t, files, providerCommand(t, files, "block", 0))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	providerPID := waitForPIDPath(t, files.pid)
	if err := syscall.Kill(providerPID, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := waitCommand(cmd, 5*time.Second); exitCode(err) != 143 {
		t.Fatalf("supervisor exit = %d, want 143 (%v)", exitCode(err), err)
	}
	assertReporter(t, files, 143, 1, true)
}

func TestRunSupervisorTERMSupervisorForwardsToProvider(t *testing.T) {
	files := newLaunchFiles(t)
	cmd := supervisorProcess(t, files, providerCommand(t, files, "block", 0))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	providerPID := waitForPIDPath(t, files.pid)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := waitCommand(cmd, 5*time.Second); exitCode(err) != 143 {
		t.Fatalf("supervisor exit = %d, want 143 (%v)", exitCode(err), err)
	}
	assertProcessGone(t, providerPID)
	assertReporter(t, files, 143, 1, true)
}

func TestRunSupervisorINTSupervisorForwardsToProvider(t *testing.T) {
	files := newLaunchFiles(t)
	cmd := supervisorProcess(t, files, providerCommand(t, files, "block", 0))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	providerPID := waitForPIDPath(t, files.pid)
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if err := waitCommand(cmd, 5*time.Second); exitCode(err) != 130 {
		t.Fatalf("supervisor exit = %d, want 130 (%v)", exitCode(err), err)
	}
	assertProcessGone(t, providerPID)
	assertReporter(t, files, 130, 1, true)
}

func TestRunSupervisorPreservesPTYStdin(t *testing.T) {
	files := newLaunchFiles(t)
	cmd := supervisorProcess(t, files, providerCommand(t, files, "read", 0))
	terminal, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	defer tty.Close()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = waitForPIDPath(t, files.pid)
	if _, err := terminal.Write([]byte("z\n")); err != nil {
		t.Fatal(err)
	}
	if err := waitCommand(cmd, 5*time.Second); err != nil {
		t.Fatalf("supervisor failed: %v", err)
	}
	data, err := os.ReadFile(files.stdin)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "z" {
		t.Fatalf("provider read %q, want z", data)
	}
	assertReporter(t, files, 0, 1, true)
}

func TestInstantExitReceiptSurvivesLateDiscovery(t *testing.T) {
	files := newLaunchFiles(t)
	cmd := supervisorProcess(t, files, providerCommand(t, files, "exit", 9))
	if err := cmd.Run(); exitCode(err) != 9 {
		t.Fatalf("supervisor exit = %d, want 9 (%v)", exitCode(err), err)
	}
	if _, err := os.Stat(files.pid); err != nil {
		t.Fatalf("instant-exit pid receipt missing: %v", err)
	}

	t.Setenv("GROVE_HOME", files.groveHome)
	pid, err := WaitForPID(context.Background(), files.jobID)
	if err != nil {
		t.Fatal(err)
	}
	if pid <= 0 {
		t.Fatalf("invalid provider PID %d", pid)
	}
	assertReporter(t, files, 9, 1, true)
	if err := CleanupPIDFile(files.jobID); err != nil {
		t.Fatal(err)
	}
}

func TestWaitForPIDAcknowledgementCleansAfterReporter(t *testing.T) {
	files := newLaunchFiles(t)
	t.Setenv("GROVE_HOME", files.groveHome)
	cmd := supervisorProcess(t, files, providerCommand(t, files, "block", 0))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid, err := WaitForPID(context.Background(), files.jobID)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	_ = waitCommand(cmd, 5*time.Second)
	assertReporter(t, files, 143, 1, true)
	if _, err := os.Stat(files.pid); !os.IsNotExist(err) {
		t.Fatalf("acknowledged pid receipt remains: %v", err)
	}
}

func TestBuildSupervisedAgentCommandQuotesAllLayers(t *testing.T) {
	root := filepath.Join(t.TempDir(), "space ' dollar$ tick`")
	groveHome := filepath.Join(root, "grove home ' $ `")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GROVE_HOME", groveHome)
	files := launchFiles{
		jobID:     "job ' dollar$ tick`",
		groveHome: groveHome,
		pid:       PidFilePath("job ' dollar$ tick`"),
		provider:  filepath.Join(root, "provider pid ' $ `"),
		reporter:  filepath.Join(root, "report ' $ `"),
		stdin:     filepath.Join(root, "stdin ' $ `"),
	}

	shim := filepath.Join(root, "supervisor shim ' $ `")
	shimScript := "#!/bin/sh\n" +
		"while [ $# -gt 0 ]; do\n" +
		" case \"$1\" in\n" +
		"  --pid-file) export HELPER_PID_FILE=\"$2\";;\n" +
		"  --agent-command) export HELPER_AGENT_COMMAND=\"$2\";;\n" +
		"  --reporter-command) export HELPER_REPORTER_COMMAND=\"$2\";;\n" +
		" esac; shift 2\n" +
		"done\n" +
		"export " + helperEnv + "=supervisor\n" +
		"exec " + shellSingleQuote(os.Args[0]) + "\n"
	if err := os.WriteFile(shim, []byte(shimScript), 0o755); err != nil {
		t.Fatal(err)
	}

	agent := providerCommand(t, files, "exit", 17)
	reporter := reporterCommand(files)
	command := BuildSupervisedAgentCommand(files.jobID, shellSingleQuote(shim), agent, reporter)
	cmd := exec.Command("sh", "-c", command)
	if err := cmd.Run(); exitCode(err) != 17 {
		t.Fatalf("quoted command exit = %d, want 17 (%v)\ncommand: %s", exitCode(err), err, command)
	}
	assertReporter(t, files, 17, 1, true)
}

type launchFiles struct {
	jobID, groveHome, pid, provider, reporter, stdin string
}

func newLaunchFiles(t *testing.T) launchFiles {
	t.Helper()
	root := t.TempDir()
	jobID := "launch-test"
	groveHome := filepath.Join(root, "home")
	return launchFiles{
		jobID:     jobID,
		groveHome: groveHome,
		pid:       filepath.Join(groveHome, "run", "grove-agent-"+jobID+".pid"),
		provider:  filepath.Join(root, "provider.pid"),
		reporter:  filepath.Join(root, "reporter.log"),
		stdin:     filepath.Join(root, "stdin.byte"),
	}
}

func supervisorProcess(t *testing.T, files launchFiles, agentCommand string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(),
		helperEnv+"=supervisor",
		"HELPER_PID_FILE="+files.pid,
		"HELPER_AGENT_COMMAND="+agentCommand,
		"HELPER_REPORTER_COMMAND="+reporterCommand(files),
	)
	return cmd
}

func providerCommand(t *testing.T, files launchFiles, mode string, rc int) string {
	t.Helper()
	return strings.Join([]string{
		"env",
		helperEnv + "=provider",
		"HELPER_PROVIDER_MODE=" + shellSingleQuote(mode),
		"HELPER_PROVIDER_PID=" + shellSingleQuote(files.provider),
		"HELPER_STDIN_FILE=" + shellSingleQuote(files.stdin),
		"HELPER_PROVIDER_RC=" + strconv.Itoa(rc),
		shellSingleQuote(os.Args[0]),
	}, " ")
}

func reporterCommand(files launchFiles) string {
	return strings.Join([]string{
		"env",
		helperEnv + "=reporter",
		"HELPER_REPORT_FILE=" + shellSingleQuote(files.reporter),
		"HELPER_REPORT_PID_FILE=" + shellSingleQuote(files.pid),
		shellSingleQuote(os.Args[0]),
	}, " ")
}

func runProviderHelper() {
	_ = os.WriteFile(os.Getenv("HELPER_PROVIDER_PID"), []byte(strconv.Itoa(os.Getpid())), 0o600)
	switch os.Getenv("HELPER_PROVIDER_MODE") {
	case "exit":
		rc, _ := strconv.Atoi(os.Getenv("HELPER_PROVIDER_RC"))
		os.Exit(rc)
	case "read":
		one := make([]byte, 1)
		if _, err := io.ReadFull(os.Stdin, one); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(91)
		}
		_ = os.WriteFile(os.Getenv("HELPER_STDIN_FILE"), one, 0o600)
		os.Exit(0)
	default:
		select {}
	}
}

func runReporterHelper() {
	rc := os.Args[len(os.Args)-1]
	_, pidErr := os.Stat(os.Getenv("HELPER_REPORT_PID_FILE"))
	line := rc + " pidfile=" + strconv.FormatBool(pidErr == nil) + "\n"
	file, err := os.OpenFile(os.Getenv("HELPER_REPORT_FILE"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(92)
	}
	_, _ = file.WriteString(line)
	_ = file.Close()
	os.Exit(0)
}

func waitForFile(path string, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			return data, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil, fmt.Errorf("timed out waiting for %s", path)
}

func waitForPIDPath(t *testing.T, path string) int {
	t.Helper()
	pid, err := waitForPIDPathResult(path, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func waitForPIDPathResult(path string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pid, ok := readPID(path); ok {
			return pid, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return 0, fmt.Errorf("timed out waiting for %s", path)
}

func waitCommand(cmd *exec.Cmd, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		return fmt.Errorf("timed out waiting for command")
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return -2
}

func assertReporter(t *testing.T, files launchFiles, rc, count int, pidfilePresent bool) {
	t.Helper()
	file, err := os.Open(files.reporter)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(lines) != count {
		t.Fatalf("reporter calls = %d, want %d: %v", len(lines), count, lines)
	}
	want := fmt.Sprintf("%d pidfile=%t", rc, pidfilePresent)
	if lines[0] != want {
		t.Fatalf("reporter line = %q, want %q", lines[0], want)
	}
}

func assertProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("provider process %d still exists", pid)
}

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
