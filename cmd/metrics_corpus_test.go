package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/eval/pkg/record"
)

const (
	emittedArmFixture = "../pkg/transcript/testdata/pi/trees/grove_emitted.jsonl"
	forkFixture       = "../pkg/transcript/testdata/pi/trees/fork_two_arm.jsonl"
	unattributedArm   = "../pkg/transcript/testdata/pi/trees/grove_unattributed.jsonl"
	// Attributed (carries a grove_config) but stamps NO replicate.
	noReplicateArm = "../pkg/transcript/testdata/pi/trees/grove_nested.jsonl"
	// Attributed, stamps a declared-EMPTY bundle manifest and no grove_metric.
	emptyBundleArm = "../pkg/transcript/testdata/pi/trees/grove_empty_bundle.jsonl"
	// Attributed, but its config vector has NO `context` entry at all: this arm
	// is not varying on the context axis and must be excluded from grouping.
	noContextComponentArm = "../pkg/transcript/testdata/pi/trees/grove_no_context_component.jsonl"
	// Attributed and stamps an EXPLICIT empty `context`. This is a MEASURED
	// empty — a real condition — and must be grouped, not excluded. It is the
	// control that keeps the exclusion above from over-reaching.
	emptyContextComponentArm = "../pkg/transcript/testdata/pi/trees/grove_empty_context_component.jsonl"
)

// piShapedCopy places a fixture at a real-looking pi session path so the
// provider inference resolves it as pi.
func piShapedCopy(t *testing.T, fixture string) string {
	t.Helper()
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, ".pi", "agent", "sessions", "--Users-test-project--")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	path := filepath.Join(sessionDir, "2026-07-01T10-00-00-000Z_0198c2f4-9a51-7abc-8def-abcabcabcabc.jsonl")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// runMetricsExpectingError executes the verb and returns its error.
func runMetricsExpectingError(t *testing.T, args ...string) error {
	t.Helper()
	cmd := newMetricsCmd()
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

// --- P6-13: the flag contract, tested through the VERB ---------------------

// --by-config groups a corpus and takes no spec. Combining them must be
// REJECTED, not silently resolved by ignoring one of the two.
func TestMetricsRejectsByConfigWithASpec(t *testing.T) {
	err := runMetricsExpectingError(t, piShapedCopy(t, emittedArmFixture), "--by-config", "context")
	if err == nil {
		t.Fatal("expected an error combining --by-config with a <spec>")
	}
	if !strings.Contains(err.Error(), "takes no <spec>") {
		t.Errorf("error = %v; it should explain the conflict", err)
	}
}

// An unknown component must be rejected with the vocabulary named, rather than
// producing one empty group that reads as "measured, found nothing".
func TestMetricsRejectsUnknownComponent(t *testing.T) {
	err := runMetricsExpectingError(t, "--by-config", "not_a_component")
	if err == nil {
		t.Fatal("expected an error for an unknown component")
	}
	if !strings.Contains(err.Error(), "unknown config component") {
		t.Errorf("error = %v", err)
	}
}

// The single-session path still requires a spec.
func TestMetricsRequiresSpecWithoutCorpusMode(t *testing.T) {
	if err := runMetricsExpectingError(t); err == nil {
		t.Fatal("expected an error with no spec and no corpus flag")
	}
}

// --- P6-16: --emit-partials through the verb -------------------------------

// The partial must carry envelope + ComponentMetrics and NOTHING else.
//
// C1 makes agentlogs the sole per-arm ComponentMetrics writer and the fork
// runner the sole Cost writer. If a partial ever carries Cost, the join stops
// being two disjoint axes and becomes a conflict.
//
// Mandatory mutation: make --emit-partials populate Cost -> this must FAIL.
func TestEmitPartialsWritesEnvelopeAndMetricsOnly(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "partials")
	path := piShapedCopy(t, emittedArmFixture)

	if err := runMetricsExpectingError(t, path, "--emit-partials", outDir); err != nil {
		t.Fatalf("metrics --emit-partials: %v", err)
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("read partials dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("wrote %d partials, want 1", len(entries))
	}

	raw, err := os.ReadFile(filepath.Join(outDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read partial: %v", err)
	}

	// Decode into a map so an ABSENT key is distinguishable from a zero one.
	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("partial is not valid JSON: %v\n%s", err, raw)
	}
	for _, forbidden := range []string{"cost", "outcome", "adherence_det", "adherence_judged", "process"} {
		if _, present := decoded[forbidden]; present {
			t.Errorf("partial carries %q; agentlogs writes envelope + component_metrics ONLY "+
				"(the fork runner owns cost, eval owns outcome)", forbidden)
		}
	}
	for _, required := range []string{"schema", "key", "config"} {
		if _, present := decoded[required]; !present {
			t.Errorf("partial is missing envelope field %q", required)
		}
	}

	var partial record.RunRecord
	if err := json.Unmarshal(raw, &partial); err != nil {
		t.Fatalf("partial does not decode as a RunRecord: %v", err)
	}
	if partial.Cost != nil {
		t.Error("Cost is non-nil; the fork runner is the sole Cost writer")
	}
	if partial.Key.TaskID != "t-042" {
		t.Errorf("Key.TaskID = %q, want t-042", partial.Key.TaskID)
	}
	if partial.Key.ConfigHash != partial.Config.Hash() {
		t.Errorf("Key.ConfigHash %q != Config.Hash() %q — the joiner keys on this",
			partial.Key.ConfigHash, partial.Config.Hash())
	}
	// Wave one's honest outcome: no component metrics exist to write.
	if len(partial.ComponentMetrics) != 0 {
		t.Errorf("ComponentMetrics = %v, want empty in wave one", partial.ComponentMetrics)
	}
}

// --emit-partials must work WITHOUT --branches: a sweep arm is a whole file,
// read by the ordinary active-path fold. If it required --branches the sweep
// could not use it at all.
func TestEmitPartialsWorksWithoutBranches(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "partials")
	path := piShapedCopy(t, emittedArmFixture)

	if err := runMetricsExpectingError(t, path, "--emit-partials", outDir); err != nil {
		t.Fatalf("--emit-partials without --branches failed: %v", err)
	}
	entries, _ := os.ReadDir(outDir)
	if len(entries) != 1 {
		t.Fatalf("wrote %d partials without --branches, want 1", len(entries))
	}
}

// An unattributed arm has no condition to key on and must be EXCLUDED from
// record emission rather than emitted under a synthesised config.
//
// THE SKIP REASON IS ASSERTED, NOT JUST THE SKIP. writeArmPartials now has TWO
// sequential skip gates — unattributed, then no-replicate-stamp — and an
// unattributed arm trips BOTH (its replicate is lifted from the grove_config
// entry it does not have). Asserting only "0 partials written" therefore cannot
// distinguish the attributed gate being present from it being deleted: the
// replicate gate catches the same fixture either way. Checking WHICH gate fired
// is what keeps this test load-bearing.
//
// Mandatory mutation: replace the `!arm.Meta.Attributed()` condition with
// `false` -> this must FAIL (the arm is still skipped, but for the wrong
// reason).
func TestEmitPartialsSkipsUnattributedArms(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "partials")
	path := piShapedCopy(t, unattributedArm)

	var runErr error
	stderr := captureStderr(t, func() {
		runErr = runMetricsExpectingError(t, path, "--emit-partials", outDir)
	})
	if runErr != nil {
		t.Fatalf("metrics: %v", runErr)
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("wrote %d partials for an unattributed arm, want 0", len(entries))
	}
	if !strings.Contains(stderr, "unattributed") {
		t.Errorf("the arm was not skipped as UNATTRIBUTED; the attributed gate did "+
			"not fire and the skip came from a later predicate. stderr:\n%s", stderr)
	}
}

// An ATTRIBUTED arm that stamps no replicate has no join key, and one cannot be
// synthesised. It must be skipped with a warning, never emitted with a
// defaulted 0.
//
// record.RunKey.Replicate is a non-nilable int, so a defaulted 0 makes an
// UNSTAMPED arm produce a RunKey byte-identical to that of an arm genuinely
// stamped `replicate: 0` under the same config — and the joiner, which keys on
// exactly that, merges two distinct experimental conditions into one. That is a
// direct recurrence of P1's blocker, the defect this framework exists to
// prevent. Losing one arm loudly beats corrupting the matrix silently.
//
// Mandatory mutation: restore `replicate := 0; if arm.Meta.Replicate != nil
// {...}` in armPartial and drop the skip -> this must FAIL.
func TestEmitPartialsSkipsAttributedArmsWithNoReplicateStamp(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "partials")
	path := piShapedCopy(t, noReplicateArm)

	var runErr error
	stderr := captureStderr(t, func() {
		runErr = runMetricsExpectingError(t, path, "--emit-partials", outDir)
	})
	if runErr != nil {
		t.Fatalf("metrics: %v", runErr)
	}

	// Guard the fixture's premise: attributed, but no replicate. If this ever
	// stops holding the test below is vacuous.
	arms := liftSessionFile(path, false)
	if len(arms) != 1 {
		t.Fatalf("fixture yielded %d arms, want 1", len(arms))
	}
	if !arms[0].Meta.Attributed() {
		t.Fatal("fixture premise broken: the arm must be ATTRIBUTED (an " +
			"unattributed arm would be skipped by the pre-existing check, making " +
			"this test prove nothing about the replicate gate)")
	}
	if arms[0].Meta.Replicate != nil {
		t.Fatalf("fixture premise broken: the arm must stamp NO replicate, got %d",
			*arms[0].Meta.Replicate)
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		raw, _ := os.ReadFile(filepath.Join(outDir, entries[0].Name()))
		t.Errorf("wrote %d partials for an arm with no replicate stamp, want 0. "+
			"A defaulted replicate collides with a real replicate 0 under the same "+
			"config and the joiner merges two conditions.\n%s", len(entries), raw)
	}
	if !strings.Contains(stderr, "no replicate stamp") {
		t.Errorf("the skip must be reported on stderr, not silent; got:\n%s", stderr)
	}
}

// The mirror-image check: a REAL stamped replicate 0 must still be emitted. The
// D4 rule cuts both ways — discarding a genuine measured zero as "not measured"
// is the same class of error as fabricating one.
func TestEmitPartialsKeepsAnExplicitReplicateZero(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "partials")
	path := piShapedCopy(t, emittedArmFixture)

	if err := runMetricsExpectingError(t, path, "--emit-partials", outDir); err != nil {
		t.Fatalf("metrics: %v", err)
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("wrote %d partials for an explicitly-stamped replicate 0, want 1. "+
			"A stamped 0 is a real value, not an absent one.", len(entries))
	}
	raw, _ := os.ReadFile(filepath.Join(outDir, entries[0].Name()))
	var partial record.RunRecord
	if err := json.Unmarshal(raw, &partial); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if partial.Key.Replicate != 0 {
		t.Errorf("Key.Replicate = %d, want 0", partial.Key.Replicate)
	}
}

// --- E4: the --by-config aggregation path ----------------------------------

// piCorpusRoot writes fixtures into a temp HOME laid out as pi's on-disk
// corpus, and returns that home. This is what the collectPiCorpusFrom seam
// exists for: with os.UserHomeDir() called inline, groupByComponent/summarise
// were unreachable from any test and the whole --by-config path shipped
// uncovered.
func piCorpusRoot(t *testing.T, fixtures ...string) string {
	t.Helper()
	home := t.TempDir()
	for i, fixture := range fixtures {
		sessionDir := filepath.Join(home, ".pi", "agent", "sessions",
			fmt.Sprintf("--Users-test-p%d--", i))
		if err := os.MkdirAll(sessionDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		data, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatalf("read fixture %s: %v", fixture, err)
		}
		name := fmt.Sprintf("2026-07-01T10-00-0%d-000Z_0198c2f4-9a51-7abc-8def-00000000000%d.jsonl", i, i)
		if err := os.WriteFile(filepath.Join(sessionDir, name), data, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return home
}

// The scan reaches every per-cwd session directory, and an unattributed arm is
// COUNTED rather than bucketed under a default condition.
func TestByConfigGroupsAttributedArmsAndCountsTheRest(t *testing.T) {
	home := piCorpusRoot(t, emittedArmFixture, noReplicateArm, unattributedArm)

	arms, err := collectPiCorpusFrom(home, false)
	if err != nil {
		t.Fatalf("collectPiCorpusFrom: %v", err)
	}
	if len(arms) != 3 {
		t.Fatalf("scanned %d arms across 3 session dirs, want 3", len(arms))
	}

	report := groupByComponent(arms, "context")
	if report.ArmsTotal != 3 {
		t.Errorf("ArmsTotal = %d, want 3", report.ArmsTotal)
	}
	if report.ArmsAttributed != 2 {
		t.Errorf("ArmsAttributed = %d, want 2", report.ArmsAttributed)
	}
	if report.ArmsUnattributed != 1 {
		t.Errorf("ArmsUnattributed = %d, want 1. An arm with no config stamp must be "+
			"counted, never bucketed under a default condition.", report.ArmsUnattributed)
	}
	// The two attributed fixtures declare different `context` hashes, so they
	// must land in different groups rather than being collapsed.
	if len(report.Groups) != 2 {
		t.Errorf("got %d groups, want 2 (distinct context hashes must not collapse): %v",
			len(report.Groups), report.Groups)
	}
	for hash, group := range report.Groups {
		if group.ComponentHash != hash {
			t.Errorf("group keyed %q carries ComponentHash %q", hash, group.ComponentHash)
		}
		if len(group.ConfigHashes) == 0 {
			t.Errorf("group %q lists no full config hashes; arms can agree on one "+
				"component and differ elsewhere, and that must stay visible", hash)
		}
	}
}

// THE D4 CLAIM THE CODE ADVERTISES: "A key absent from an arm is NOT counted as
// zero — n records how many arms actually reported it."
//
// grove_nested stamps two grove_metric keys; grove_empty_bundle stamps none.
// Grouped together, the summary's N must be 1 (the reporting arm), NOT 2 with a
// halved mean.
//
// Mandatory mutation: make groupByComponent append 0 for a non-reporting arm ->
// this must FAIL.
func TestByConfigDoesNotCountAbsentMetricsAsZero(t *testing.T) {
	home := piCorpusRoot(t, noReplicateArm, emptyBundleArm)

	arms, err := collectPiCorpusFrom(home, false)
	if err != nil {
		t.Fatalf("collectPiCorpusFrom: %v", err)
	}
	// Force both arms into ONE group so the summary spans a reporter and a
	// non-reporter.
	for i := range arms {
		arms[i].Meta.Config.Components["context"] = "shared"
	}

	report := groupByComponent(arms, "context")
	group, ok := report.Groups["shared"]
	if !ok {
		t.Fatalf("no 'shared' group; got %v", report.Groups)
	}
	if group.N != 2 {
		t.Fatalf("group N = %d, want 2 arms", group.N)
	}

	summary, ok := group.ComponentMetrics["context.off_bundle_reads"]
	if !ok {
		t.Fatalf("context.off_bundle_reads missing; got %v", group.ComponentMetrics)
	}
	if summary.N != 1 {
		t.Errorf("summary.N = %d, want 1. Only ONE of the two arms reported this "+
			"key; counting the silent arm as a 0 would both inflate N and halve the "+
			"mean, turning 'not measured' into a measurement.", summary.N)
	}
	if summary.Mean != 3 {
		t.Errorf("Mean = %v, want 3 (the single reported value, undiluted)", summary.Mean)
	}
}

// E4 DECISION: a metric NO arm reported serialises ABSENT, not as a zeroed
// summary. {"n":0,"mean":0,"min":0,"max":0} states a mean of 0 for something
// nothing measured — the same not-measured-reads-as-zero substitution this
// phase exists to stop — disambiguated only by a reader who notices n:0.
//
// Mandatory mutation: drop the pointer/omitempty on ToolCalls -> this must FAIL.
func TestByConfigOmitsSummariesNoArmReported(t *testing.T) {
	home := piCorpusRoot(t, emittedArmFixture)
	arms, err := collectPiCorpusFrom(home, false)
	if err != nil {
		t.Fatalf("collectPiCorpusFrom: %v", err)
	}
	// Strip the process fold so nothing reports tool calls or turns.
	for i := range arms {
		arms[i].Process.ToolCalls = nil
		arms[i].Process.Turns = nil
	}

	raw, err := json.Marshal(groupByComponent(arms, "context"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	groups := decoded["groups"].(map[string]interface{})
	for hash, g := range groups {
		group := g.(map[string]interface{})
		for _, key := range []string{"tool_calls", "turns"} {
			if _, present := group[key]; present {
				t.Errorf("group %q serialises %q despite no arm reporting it: %v. "+
					"Not-measured must be ABSENT, not a zeroed summary.",
					hash, key, group[key])
			}
		}
	}
}

// The mirror-image: a REAL measured value still serialises, including a real 0.
func TestByConfigKeepsSummariesThatWereMeasured(t *testing.T) {
	home := piCorpusRoot(t, emittedArmFixture)
	arms, err := collectPiCorpusFrom(home, false)
	if err != nil {
		t.Fatalf("collectPiCorpusFrom: %v", err)
	}
	zero := 0
	for i := range arms {
		arms[i].Process.ToolCalls = &zero
	}

	report := groupByComponent(arms, "context")
	for hash, group := range report.Groups {
		if group.ToolCalls == nil {
			t.Fatalf("group %q dropped a MEASURED tool-call count of 0. A real "+
				"measured zero is not the same as not-measured (D4 cuts both ways).",
				hash)
		}
		if group.ToolCalls.N == 0 {
			t.Errorf("group %q reports N=0 for a value an arm did report", hash)
		}
		if group.ToolCalls.Mean != 0 {
			t.Errorf("group %q Mean = %v, want 0", hash, group.ToolCalls.Mean)
		}
	}
}

// summarise's contract, pinned directly: no values -> nil (absent), values ->
// a summary whose N is the number of CONTRIBUTORS.
func TestSummariseDistinguishesAbsentFromZero(t *testing.T) {
	if got := summarise(nil); got != nil {
		t.Errorf("summarise(nil) = %+v, want nil (not measured)", *got)
	}
	if got := summarise([]float64{}); got != nil {
		t.Errorf("summarise([]) = %+v, want nil (not measured)", *got)
	}
	got := summarise([]float64{0})
	if got == nil {
		t.Fatal("summarise([0]) = nil; a measured zero IS a measurement")
	}
	if got.N != 1 || got.Mean != 0 || got.Min != 0 || got.Max != 0 {
		t.Errorf("summarise([0]) = %+v, want N=1 mean/min/max=0", *got)
	}
	got = summarise([]float64{1, 5, 3})
	if got.N != 3 || got.Mean != 3 || got.Min != 1 || got.Max != 5 {
		t.Errorf("summarise([1 5 3]) = %+v, want N=3 mean=3 min=1 max=5", *got)
	}
}

// --- P6-13: --branches through the verb ------------------------------------

// --branches folds each arm separately, and each arm's cost is its OWN path —
// not the whole-file sum.
func TestBranchesFoldsEachArmSeparately(t *testing.T) {
	path := piShapedCopy(t, forkFixture)

	out := captureStdout(t, func() {
		if err := runMetricsExpectingError(t, path, "--branches", "--json"); err != nil {
			t.Fatalf("metrics --branches: %v", err)
		}
	})

	var arms []armSummary
	if err := json.Unmarshal([]byte(out), &arms); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(arms) != 2 {
		t.Fatalf("got %d arms, want 2", len(arms))
	}

	// Arm B: seed 100 + 500. Arm C: seed 100 + 700.
	if arms[0].InputTokens != 600 {
		t.Errorf("arm B InputTokens = %d, want 600", arms[0].InputTokens)
	}
	if arms[1].InputTokens != 700+100 {
		t.Errorf("arm C InputTokens = %d, want 800", arms[1].InputTokens)
	}
	// Neither arm may report the whole-file sum.
	for i, arm := range arms {
		if arm.InputTokens == 1300 {
			t.Errorf("arm %d reported the whole-file sum; the fold is not path-scoped", i)
		}
	}
}

// Without --branches only the active path is folded.
func TestWithoutBranchesOnlyActivePathIsFolded(t *testing.T) {
	path := piShapedCopy(t, forkFixture)

	out := captureStdout(t, func() {
		if err := runMetricsExpectingError(t, path, "--json", "--emit-partials", filepath.Join(t.TempDir(), "p")); err != nil {
			t.Fatalf("metrics: %v", err)
		}
	})

	var arms []armSummary
	if err := json.Unmarshal([]byte(out), &arms); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	if len(arms) != 1 {
		t.Fatalf("got %d arms without --branches, want 1 (the active path)", len(arms))
	}
	if arms[0].InputTokens != 800 {
		t.Errorf("active arm InputTokens = %d, want 800", arms[0].InputTokens)
	}
}

// captureStderr runs fn with stderr redirected and returns what it printed.
// The partial-skip reports go to stderr, and "was it reported at all" is the
// whole point of skipping loudly rather than silently.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	fn()
	_ = w.Close()
	os.Stderr = orig
	return <-done
}

// captureStdout runs fn with stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	fn()
	_ = w.Close()
	os.Stdout = orig
	return <-done
}

// AN ARM WITH NO ENTRY FOR THE GROUPED COMPONENT IS NOT VARYING ON THAT AXIS,
// and it must be excluded from grouping rather than merged into a "" bucket.
//
// The defect this pins: `hash := arm.Meta.Config.Components[component]` returns
// "" for BOTH "stamped empty" and "never stamped", so an arm with no context
// component and an arm stamping `"context": ""` collapsed into ONE group keyed
// "" with n:2 — a full statistical summary for a condition that was never
// varied on the requested axis. The field's own comment claimed the hash was
// "absent when the arm's vector has no entry for it" while the tag carried no
// omitempty, so it always serialised.
//
// Both halves of the ruling are asserted here, and the second is what stops the
// fix from over-reaching: the not-measured arm is EXCLUDED and COUNTED, and the
// measured-empty arm STAYS. D4 cuts both ways — a real measured empty must
// never be discarded as not-measured.
//
// Mandatory mutation: revert to a plain map index (drop the comma-ok and the
// ArmsNoComponent skip) -> the "" group carries n:2 and this must FAIL.
func TestByConfigExcludesArmsNotVaryingOnTheComponent(t *testing.T) {
	home := piCorpusRoot(t, noContextComponentArm, emptyContextComponentArm)

	arms, err := collectPiCorpusFrom(home, false)
	if err != nil {
		t.Fatalf("collectPiCorpusFrom: %v", err)
	}
	if len(arms) != 2 {
		t.Fatalf("scanned %d arms, want 2", len(arms))
	}

	report := groupByComponent(arms, "context")

	if report.ArmsAttributed != 2 {
		t.Errorf("ArmsAttributed = %d, want 2 (both arms carry a config stamp; "+
			"lacking one COMPONENT is not the same as lacking attribution)",
			report.ArmsAttributed)
	}
	if report.ArmsNoComponent != 1 {
		t.Errorf("ArmsNoComponent = %d, want 1. The arm whose vector has no "+
			"`context` entry must be excluded from grouping AND counted, so the "+
			"exclusion is visible rather than silent.", report.ArmsNoComponent)
	}

	// Exactly one group: the measured-empty arm. The not-measured arm is gone.
	if len(report.Groups) != 1 {
		t.Fatalf("got %d groups, want 1: %v", len(report.Groups), report.Groups)
	}
	group, ok := report.Groups[""]
	if !ok {
		t.Fatalf("no \"\" group; an arm stamping `\"context\": \"\"` is a MEASURED "+
			"empty and a real condition — it must be grouped, not excluded: %v",
			report.Groups)
	}
	if group.N != 1 {
		t.Errorf("group \"\" has N = %d, want 1. An N of 2 means the arm with NO "+
			"context entry was merged in with the arm stamping an explicit \"\", "+
			"producing a statistical summary over a condition that was never "+
			"varied on this axis.", group.N)
	}
	if group.SessionIDs[0] != "0198c2f4-9a51-7abc-8def-000000000001" {
		t.Errorf("the \"\" group holds %v; want only the measured-empty arm",
			group.SessionIDs)
	}
}

// THE ACCEPTANCE MUTATION, ON THE WIRE: the two states must be distinguishable
// in SERIALISED output, not merely in Go values. `component_hash` without
// omitempty rendered a not-measured hash and a measured-empty hash as the same
// bytes — `"component_hash": ""` — so a reader of the JSON could not tell them
// apart even when the Go side could.
//
// Mandatory mutation: drop omitempty from ComponentHash -> the measured-empty
// group serialises `"component_hash": ""` and this must FAIL.
func TestByConfigSerialisationDistinguishesNotMeasuredFromMeasuredEmpty(t *testing.T) {
	home := piCorpusRoot(t, noContextComponentArm, emptyContextComponentArm)
	arms, err := collectPiCorpusFrom(home, false)
	if err != nil {
		t.Fatalf("collectPiCorpusFrom: %v", err)
	}

	raw, err := json.Marshal(groupByComponent(arms, "context"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// The not-measured arm is visible as a count, and nowhere else.
	if n, _ := decoded["arms_no_component"].(float64); n != 1 {
		t.Errorf("serialised arms_no_component = %v, want 1. The exclusion must be "+
			"legible to a reader of the JSON, not just to the Go caller.",
			decoded["arms_no_component"])
	}

	groups := decoded["groups"].(map[string]interface{})
	if len(groups) != 1 {
		t.Fatalf("serialised %d groups, want 1: %v", len(groups), groups)
	}
	group, ok := groups[""].(map[string]interface{})
	if !ok {
		t.Fatalf("no \"\" group in serialised output: %v", groups)
	}
	if _, present := group["component_hash"]; present {
		t.Errorf("the measured-empty group serialises component_hash = %v. With no "+
			"omitempty an empty hash is indistinguishable on the wire from a "+
			"not-measured one; the group's identity lives in its map key.",
			group["component_hash"])
	}
	if n, _ := group["n"].(float64); n != 1 {
		t.Errorf("serialised group \"\" has n = %v, want 1", group["n"])
	}
}
