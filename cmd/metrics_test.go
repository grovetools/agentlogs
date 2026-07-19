package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/grovetools/agentlogs/internal/provider"
	"github.com/grovetools/agentlogs/pkg/metrics"
)

const (
	claudeFixture = "../pkg/metrics/testdata/claude/session-metrics-fixture.jsonl"
	codexFixture  = "../pkg/transcript/testdata/codex/sessions/2026/07/01/rollout-2026-07-01T10-00-00-5973b6c0-94b8-487b-a530-2aeb6098ae0e.jsonl"
	piFixture     = "../pkg/transcript/testdata/pi/sessions/--Users-test-project--/2026-07-01T10-00-00-000Z_0198c2f4-9a51-7abc-8def-0123456789ab.jsonl"
)

// iv/fv dereference the D4 "nil = not measured" pointers for assertions,
// mapping nil to a sentinel that can never equal a legitimate value so a
// silently-unmeasured field fails the test rather than reading as zero.
func iv(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

func fv(p *float64) float64 {
	if p == nil {
		return -1
	}
	return *p
}

// computeFixture loads a fixture through the same file-based path the command
// uses — SelectSource with a nil daemon client — and folds it.
func computeFixture(t *testing.T, path string) metrics.Result {
	t.Helper()

	info, err := resolveMetricsSession(path)
	if err != nil {
		t.Fatalf("resolveMetricsSession(%s): %v", path, err)
	}

	src := provider.SelectSource(info, nil)
	entries, err := src.Read(context.Background(), info, provider.ReadOptions{
		DetailLevel: "full",
		EndLine:     -1,
	})
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	result := metrics.Compute(entries)
	result.SessionID = info.SessionID
	return result
}

// runMetricsCmd executes the metrics subcommand and captures stdout. The
// command prints with fmt.Println (matching the tokens.go precedent), so
// stdout is redirected rather than using cmd.SetOut.
func runMetricsCmd(t *testing.T, args ...string) string {
	t.Helper()

	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	cmd := newMetricsCmd()
	cmd.SetArgs(args)
	runErr := cmd.Execute()

	_ = w.Close()
	os.Stdout = orig
	out := <-done

	if runErr != nil {
		t.Fatalf("metrics %v: %v (output: %s)", args, runErr, out)
	}
	return out
}

// --- Provider path inference ---------------------------------------------

func TestResolveMetricsSessionInfersProvider(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{claudeFixture, "claude"},
		{codexFixture, "codex"},
		{piFixture, "pi"},
	}

	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			info, err := resolveMetricsSession(tc.path)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if info.Provider != tc.want {
				t.Errorf("Provider = %q, want %q", info.Provider, tc.want)
			}
			if info.LogFilePath != tc.path {
				t.Errorf("LogFilePath = %q, want %q", info.LogFilePath, tc.path)
			}
		})
	}
}

// --- Daemon independence --------------------------------------------------

// The metrics fold must be deterministic, which means it must never read
// through a DaemonSource — otherwise the numbers would depend on whether a
// daemon happened to be running. SelectSource guards its entire daemon branch
// on `daemonClient != nil` (internal/provider/router.go), so passing nil pins
// the file-based sources. This test fails if that guard is ever relaxed.
func TestMetricsUsesFileBasedSourcesOnly(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{claudeFixture, "*provider.ClaudeSource"},
		{codexFixture, "*provider.CodexSource"},
		{piFixture, "*provider.PiSource"},
	}

	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			info, err := resolveMetricsSession(tc.path)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			src := provider.SelectSource(info, nil)
			if got := fmt.Sprintf("%T", src); got != tc.want {
				t.Errorf("SelectSource returned %s, want %s", got, tc.want)
			}
		})
	}
}

// --- Golden: claude ------------------------------------------------------

// The claude fold, end to end through the normalizer. The fixture includes a
// sidechain Read of /repo/sidechain-only.go and a Bash call, neither of which
// may appear in the file counts.
func TestGoldenClaudeFixture(t *testing.T) {
	got := computeFixture(t, claudeFixture)

	if got.Provider != "claude" {
		t.Errorf("Provider = %q, want claude", got.Provider)
	}
	// Read, Edit, Write, Grep, Bash — the sidechain Read is excluded.
	if iv(got.ToolCalls) != 5 {
		t.Errorf("ToolCalls = %d, want 5", iv(got.ToolCalls))
	}
	if iv(got.DistinctTools) != 5 {
		t.Errorf("DistinctTools = %d, want 5", iv(got.DistinctTools))
	}
	// "Fix the bug in util.go" and "Thanks, now add a README". The user
	// entries carrying only tool_result are not turns, nor is the sidechain.
	if iv(got.Turns) != 2 {
		t.Errorf("Turns = %d, want 2", iv(got.Turns))
	}

	wantTouched := []string{"/repo", "/repo/new.go", "/repo/util.go"}
	wantEdited := []string{"/repo/new.go", "/repo/util.go"}

	if got.FilesTouched == nil || *got.FilesTouched != len(wantTouched) {
		t.Fatalf("FilesTouched = %v, want %d", got.FilesTouched, len(wantTouched))
	}
	if got.FilesEdited == nil || *got.FilesEdited != len(wantEdited) {
		t.Fatalf("FilesEdited = %v, want %d", got.FilesEdited, len(wantEdited))
	}
	assertPaths(t, "touched", got.TouchedFiles, wantTouched)
	assertPaths(t, "edited", got.EditedFiles, wantEdited)

	for _, f := range got.TouchedFiles {
		if f == "/repo/sidechain-only.go" {
			t.Error("sidechain file leaked into touched files")
		}
	}
	if len(got.Unsupported) != 0 {
		t.Errorf("Unsupported = %v, want empty", got.Unsupported)
	}
	if fv(got.Diagnostics.WallClockSeconds) != 20 {
		t.Errorf("WallClockSeconds = %v, want 20", fv(got.Diagnostics.WallClockSeconds))
	}
}

func assertPaths(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s[%d] = %q, want %q", what, i, got[i], want[i])
		}
	}
}

// --- Golden: codex is unsupported in v0 ----------------------------------

// Codex exposes file work only through an argv array on `shell`, so its file
// counts must be absent rather than zero.
func TestGoldenCodexFixtureYieldsNilFileCounts(t *testing.T) {
	got := computeFixture(t, codexFixture)

	if got.Provider != "codex" {
		t.Errorf("Provider = %q, want codex", got.Provider)
	}
	if got.FilesTouched != nil {
		t.Errorf("FilesTouched = %d, want nil", *got.FilesTouched)
	}
	if got.FilesEdited != nil {
		t.Errorf("FilesEdited = %d, want nil", *got.FilesEdited)
	}
	if got.ForbiddenTouches != nil {
		t.Errorf("ForbiddenTouches = %d, want nil", *got.ForbiddenTouches)
	}

	want := []string{metrics.UnsupportedFilesTouched, metrics.UnsupportedFilesEdited}
	assertPaths(t, "unsupported", got.Unsupported, want)

	// Process metrics are still measured: shell and update_plan.
	if iv(got.ToolCalls) != 2 {
		t.Errorf("ToolCalls = %d, want 2", iv(got.ToolCalls))
	}
	if iv(got.DistinctTools) != 2 {
		t.Errorf("DistinctTools = %d, want 2", iv(got.DistinctTools))
	}
	if iv(got.Turns) != 2 {
		t.Errorf("Turns = %d, want 2", iv(got.Turns))
	}
}

func TestGoldenPiFixtureYieldsNilFileCounts(t *testing.T) {
	got := computeFixture(t, piFixture)

	if got.Provider != "pi" {
		t.Errorf("Provider = %q, want pi", got.Provider)
	}
	if got.FilesTouched != nil {
		t.Errorf("FilesTouched = %d, want nil", *got.FilesTouched)
	}
	want := []string{metrics.UnsupportedFilesTouched, metrics.UnsupportedFilesEdited}
	assertPaths(t, "unsupported", got.Unsupported, want)

	// The only pi tool is bash.
	if iv(got.ToolCalls) != 1 {
		t.Errorf("ToolCalls = %d, want 1", iv(got.ToolCalls))
	}
	if iv(got.Turns) != 2 {
		t.Errorf("Turns = %d, want 2", iv(got.Turns))
	}
}

// --- CLI ------------------------------------------------------------------

func TestMetricsCmdJSONOutput(t *testing.T) {
	out := runMetricsCmd(t, claudeFixture, "--json")

	var got metrics.Result
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}

	if got.Provider != "claude" {
		t.Errorf("provider = %q, want claude", got.Provider)
	}
	if iv(got.ToolCalls) != 5 {
		t.Errorf("tool_calls = %d, want 5", iv(got.ToolCalls))
	}
	if iv(got.DistinctTools) != 5 {
		t.Errorf("distinct_tools = %d, want 5", iv(got.DistinctTools))
	}
	if iv(got.Turns) != 2 {
		t.Errorf("turns = %d, want 2", iv(got.Turns))
	}
	if got.FilesTouched == nil || *got.FilesTouched != 3 {
		t.Errorf("files_touched = %v, want 3", got.FilesTouched)
	}
	if got.FilesEdited == nil || *got.FilesEdited != 2 {
		t.Errorf("files_edited = %v, want 2", got.FilesEdited)
	}
	// Without --files the lists are withheld.
	if len(got.TouchedFiles) != 0 || len(got.EditedFiles) != 0 {
		t.Errorf("file lists present without --files: %v / %v", got.TouchedFiles, got.EditedFiles)
	}
}

func TestMetricsCmdFilesFlagEmitsLists(t *testing.T) {
	out := runMetricsCmd(t, claudeFixture, "--json", "--files")

	var got metrics.Result
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}

	assertPaths(t, "touched_files", got.TouchedFiles,
		[]string{"/repo", "/repo/new.go", "/repo/util.go"})
	assertPaths(t, "edited_files", got.EditedFiles,
		[]string{"/repo/new.go", "/repo/util.go"})
}

// The codex ruling must survive the CLI boundary: the keys are absent from the
// JSON entirely, so a consumer cannot read them as zero.
func TestMetricsCmdCodexOmitsFileKeys(t *testing.T) {
	out := runMetricsCmd(t, codexFixture, "--json")

	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}

	for _, key := range []string{"files_touched", "files_edited", "forbidden_touches"} {
		if _, present := decoded[key]; present {
			t.Errorf("key %q present for codex, want omitted", key)
		}
	}

	unsupported, ok := decoded["unsupported"].([]interface{})
	if !ok || len(unsupported) != 2 {
		t.Fatalf("unsupported = %v, want two entries", decoded["unsupported"])
	}
	if _, ok := decoded["diagnostics"].(map[string]interface{}); !ok {
		t.Error("diagnostics sub-object missing")
	}
}

func TestMetricsCmdHumanReadableOutput(t *testing.T) {
	out := runMetricsCmd(t, codexFixture)

	for _, want := range []string{"Tool calls:", "Turns:", "not measured", "Diagnostics"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestMetricsCmdRejectsUnresolvableSpec(t *testing.T) {
	cmd := newMetricsCmd()
	cmd.SetArgs([]string{"definitely-not-a-session-or-path"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err == nil {
		t.Error("expected an error for an unresolvable spec")
	}
}
