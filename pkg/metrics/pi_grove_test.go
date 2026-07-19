package metrics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

const piTreeDir = "../transcript/testdata/pi/trees"

// liftFixture parses a tree fixture and lifts its ACTIVE PATH — which, under
// fork-per-session, is exactly what a sweep arm is.
func liftFixture(t *testing.T, name string) (*transcript.PiSessionTree, PiGroveMeta, []LiftWarning) {
	t.Helper()
	f, err := os.Open(filepath.Join(piTreeDir, name+".jsonl"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	tree, err := transcript.ParsePiSessionTree(f)
	if err != nil {
		t.Fatalf("ParsePiSessionTree: %v", err)
	}
	if tree == nil {
		t.Fatal("tree is nil")
	}
	meta, warnings := LiftGroveEntries(tree, tree.ActivePath())
	return tree, meta, warnings
}

func warningCodes(warnings []LiftWarning) []string {
	codes := make([]string, 0, len(warnings))
	for _, w := range warnings {
		codes = append(codes, w.Code)
	}
	return codes
}

func hasWarning(warnings []LiftWarning, code string) bool {
	for _, w := range warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

// --- the happy path -------------------------------------------------------

func TestLiftGroveEntriesNestedShape(t *testing.T) {
	_, meta, warnings := liftFixture(t, "grove_nested")

	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warningCodes(warnings))
	}
	if !meta.Attributed() {
		t.Fatal("Config is nil; the arm should be attributed")
	}
	if meta.Config.Model != "anthropic/claude-sonnet-4-5" {
		t.Errorf("Model = %q", meta.Config.Model)
	}
	if got := meta.Config.Components["context"]; got != "c-seed" {
		t.Errorf("components[context] = %q, want c-seed", got)
	}
	if meta.Schema != "0.2.0" {
		t.Errorf("Schema = %q, want 0.2.0", meta.Schema)
	}
	if meta.JobID != "plan/job.md" {
		t.Errorf("JobID = %q", meta.JobID)
	}
	// The stamped hash is the real ConfigVector.Hash() of the stamped vector,
	// so it must cross-check clean.
	if meta.ConfigHash != meta.Config.Hash() {
		t.Errorf("ConfigHash = %q, want %q", meta.ConfigHash, meta.Config.Hash())
	}
	if meta.Metrics["context.off_bundle_reads"] != 3 {
		t.Errorf("metrics = %v", meta.Metrics)
	}
	if meta.Metrics["skills.load_order_violations"] != 1 {
		t.Errorf("metrics = %v", meta.Metrics)
	}
}

// --- D2's validation seam -------------------------------------------------

func TestLiftGroveEntriesWarnsOnHashMismatch(t *testing.T) {
	_, meta, warnings := liftFixture(t, "grove_hash_mismatch")

	if !hasWarning(warnings, WarnConfigHashMismatch) {
		t.Fatalf("want a %s warning, got %v", WarnConfigHashMismatch, warningCodes(warnings))
	}
	// A drifted hash must not discard the vector — the config is still usable,
	// it is the emitter's hash that is suspect.
	if !meta.Attributed() {
		t.Error("Config went nil on a hash mismatch; the vector is still readable")
	}
}

// --- P6-10: the legacy flat shape -----------------------------------------

func TestLiftGroveEntriesAcceptsLegacyFlatShapeWithWarning(t *testing.T) {
	_, meta, warnings := liftFixture(t, "grove_legacy_flat")

	if !hasWarning(warnings, WarnLegacyConfigShape) {
		t.Fatalf("want a %s warning, got %v", WarnLegacyConfigShape, warningCodes(warnings))
	}
	// Refusing to read the legacy shape would discard data already sitting in
	// real corpora, so it must still produce a usable vector.
	if !meta.Attributed() {
		t.Fatal("legacy-shaped entry produced no Config")
	}
	if got := meta.Config.Components["prompt"]; got != "p-seed" {
		t.Errorf("components[prompt] = %q, want p-seed", got)
	}
	if got := meta.Config.Components["context"]; got != "c-seed" {
		t.Errorf("components[context] = %q, want c-seed", got)
	}
	// The scalars must NOT be mistaken for components.
	for _, scalar := range []string{"grove_metrics_version", "job_id", "provider"} {
		if _, present := meta.Config.Components[scalar]; present {
			t.Errorf("scalar %q leaked into Components", scalar)
		}
	}
	// The legacy shape stamps no hash, and synthesising one would assert a
	// cross-check that never happened.
	if meta.ConfigHash != "" {
		t.Errorf("ConfigHash = %q, want empty for the legacy shape", meta.ConfigHash)
	}
}

// --- D8 key grammar -------------------------------------------------------

func TestLiftGroveEntriesDropsBadMetricKeys(t *testing.T) {
	_, meta, warnings := liftFixture(t, "grove_bad_metric_key")

	if !hasWarning(warnings, WarnBadMetricKey) {
		t.Fatalf("want a %s warning, got %v", WarnBadMetricKey, warningCodes(warnings))
	}
	// Only the well-formed key survives.
	if len(meta.Metrics) != 1 {
		t.Errorf("Metrics = %v, want exactly the one valid key", meta.Metrics)
	}
	if meta.Metrics["context.off_bundle_reads"] != 2 {
		t.Errorf("Metrics = %v", meta.Metrics)
	}
	for _, bad := range []string{"nodots", "too.many.segments", "context.BadCase", ""} {
		if _, present := meta.Metrics[bad]; present {
			t.Errorf("malformed key %q was admitted", bad)
		}
	}
}

// D7 additive-read: an emitter newer than this reader may stamp fields it does
// not know, and that must not break the lift.
func TestLiftGroveEntriesIgnoresUnknownFields(t *testing.T) {
	tree := parseInline(t, `{"type":"session","version":3,"id":"0198c2f4-0000-7abc-8def-0000000000aa","timestamp":"2026-07-01T10:00:00.000Z"}
{"type":"custom","id":"x1","parentId":null,"timestamp":"2026-07-01T10:00:01.000Z","customType":"grove_config","data":{"config":{"model":"m","provider":"pi"},"schema":"0.2.0","a_field_from_the_future":{"nested":[1,2,3]},"another":"value"}}`)

	meta, warnings := LiftGroveEntries(tree, tree.ActivePath())

	if len(warnings) != 0 {
		t.Errorf("unknown fields produced warnings %v; D7 says ignore them", warningCodes(warnings))
	}
	if !meta.Attributed() || meta.Config.Model != "m" {
		t.Errorf("Config = %+v, want the known fields to survive", meta.Config)
	}
}

// --- P6-11: last-stamp-wins ------------------------------------------------

// createBranchedSession physically copies the seed prefix — including the seed's
// grove_config — into every arm file, so an arm file holds the seed stamp
// FOLLOWED BY its own variant stamp. "Last on the path wins" is what makes the
// variant take effect, so the ordering rule is load-bearing, not cosmetic.
func TestLiftGroveEntriesVariantStampOverridesCopiedSeed(t *testing.T) {
	_, meta, _ := liftFixture(t, "grove_arm_seed_then_variant")

	if !meta.Attributed() {
		t.Fatal("no Config")
	}
	if got := meta.Config.Components["context"]; got != "c-VARIANT" {
		t.Errorf("components[context] = %q, want c-VARIANT — the seed stamp won, "+
			"so every arm of a sweep would be attributed to the seed condition", got)
	}
	if meta.TaskID != "t-042" {
		t.Errorf("TaskID = %q, want t-042 (the variant's)", meta.TaskID)
	}
	if !meta.Sweep {
		t.Error("Sweep = false, want true")
	}
	if meta.Replicate == nil {
		t.Fatal("Replicate is nil; the variant stamped replicate 0")
	}
	if *meta.Replicate != 0 {
		t.Errorf("Replicate = %d, want 0", *meta.Replicate)
	}
	if meta.ConfigHash != meta.Config.Hash() {
		t.Errorf("ConfigHash %q does not match the variant vector's hash %q",
			meta.ConfigHash, meta.Config.Hash())
	}
}

// D4: replicate 0 is a real value and must be distinguishable from unstamped.
func TestLiftGroveEntriesReplicateNilWhenUnstamped(t *testing.T) {
	_, meta, _ := liftFixture(t, "grove_nested")

	if meta.Replicate != nil {
		t.Errorf("Replicate = %d, want nil — this fixture stamps none, and a 0 "+
			"here would be indistinguishable from a real replicate 0", *meta.Replicate)
	}
}

// Metrics ACCUMULATE across every grove_metric entry on the arm's path; a later
// entry must not wipe an earlier one.
//
// The nil check on meta.Metrics is what makes that true: allocating the map
// unconditionally would replace it on each entry, so only the LAST
// grove_metric's keys would survive and every earlier measurement would vanish
// silently — no warning, no absent key, just a smaller map than was reported.
//
// This is reachable in production today, not hypothetical: the emitter appends
// one grove_metric per agent_end (metrics.ts), so any multi-turn session carries
// several on one branch. It is harmless only because that payload currently
// stamps {turns, tool_counts} and no `metrics` key, so the merge has nothing to
// lose yet. It becomes silent data loss the moment a real component metric is
// emitted, which is why it is pinned now rather than then.
//
// The row this pins that no other fixture supplies: TWO grove_metric entries on
// one path, each carrying a DIFFERENT valid D8 key. Every other fixture stamps
// at most one grove_metric entry, so the second call into liftMetricEntry — the
// only call where the guard can matter — never happened.
//
// Mandatory mutation: `meta.Metrics = make(map[string]float64)` unconditionally
// (drop the nil check) -> this must FAIL.
func TestLiftGroveEntriesAccumulatesAcrossMetricEntries(t *testing.T) {
	_, meta, warnings := liftFixture(t, "grove_two_metric_entries")

	if len(warnings) != 0 {
		t.Fatalf("fixture premise broken: want no lift warnings (both keys are "+
			"valid D8), got %v", warningCodes(warnings))
	}
	if len(meta.Metrics) != 2 {
		t.Fatalf("Metrics = %v, want both keys. A second grove_metric entry must ADD "+
			"to the arm's metrics, not replace them: re-allocating the map per entry "+
			"discards every earlier measurement with no warning and no absent key.",
			meta.Metrics)
	}
	// The EARLIER entry's key is the one a re-allocating map would lose.
	if got, ok := meta.Metrics["context.off_bundle_reads"]; !ok || got != 3 {
		t.Errorf("context.off_bundle_reads = %v (present=%v), want 3. This key comes "+
			"from the FIRST grove_metric entry and is exactly what a later entry "+
			"would wipe.", got, ok)
	}
	if got, ok := meta.Metrics["skills.load_order_violations"]; !ok || got != 1 {
		t.Errorf("skills.load_order_violations = %v (present=%v), want 1", got, ok)
	}
}

// --- unattributed arms ----------------------------------------------------

func TestLiftGroveEntriesUnattributedArm(t *testing.T) {
	_, meta, _ := liftFixture(t, "grove_unattributed")

	if meta.Attributed() {
		t.Error("an arm with no grove_config must not be attributed")
	}
	if meta.Config != nil {
		t.Error("Config must be nil, not a zero-valued vector")
	}
	if meta.Metrics != nil {
		t.Errorf("Metrics = %v, want nil (never reported), not an empty map", meta.Metrics)
	}
}

// --- compaction / multi-model ----------------------------------------------

func TestLiftGroveEntriesMarksCompactedArm(t *testing.T) {
	_, meta, warnings := liftFixture(t, "compaction")

	if !meta.Compacted {
		t.Error("Compacted = false for an arm whose path holds a compaction entry")
	}
	if !hasWarning(warnings, WarnCompactedArm) {
		t.Errorf("want a %s warning, got %v", WarnCompactedArm, warningCodes(warnings))
	}
}

func TestLiftGroveEntriesWarnsOnMultiModelArm(t *testing.T) {
	_, meta, warnings := liftFixture(t, "multi_model")

	if len(meta.Models) != 2 {
		t.Fatalf("Models = %v, want 2", meta.Models)
	}
	if !hasWarning(warnings, WarnModelDisagreement) {
		t.Errorf("want a %s warning, got %v", WarnModelDisagreement, warningCodes(warnings))
	}
}

// --- the cost fold: THE distinguishing assertion ---------------------------

// The arm fold must be PATH-SCOPED, and this is the test that proves it is
// genuinely a different computation from the whole-file sum in pkg/usage.
//
// fork_two_arm bills 500+50 tokens on the ABANDONED arm B and 700+70 on the
// active arm C, plus 100+20 on the shared seed. A path-scoped fold of the active
// path sees only the seed and arm C. A whole-file sum would also swallow arm B.
//
// Mandatory mutation (P6-12): make foldArmCost sum all entries instead of path
// entries -> this test must FAIL. If it passes, the fixture has no genuinely
// abandoned billed entries and proves nothing.
func TestArmCostFoldExcludesAbandonedBranch(t *testing.T) {
	tree, _, _ := liftFixture(t, "fork_two_arm")

	active := tree.ActivePath()
	meta, _ := LiftGroveEntries(tree, active)

	// seed (100 in / 20 out) + arm C (700 in / 70 out). Arm B's 500/50 is
	// billed but belongs to a different arm.
	const wantInput, wantOutput int64 = 800, 90
	if meta.Cost.InputTokens != wantInput {
		t.Errorf("InputTokens = %d, want %d — arm B's 500 leaked into the active "+
			"arm's cost", meta.Cost.InputTokens, wantInput)
	}
	if meta.Cost.OutputTokens != wantOutput {
		t.Errorf("OutputTokens = %d, want %d", meta.Cost.OutputTokens, wantOutput)
	}

	// The abandoned arm folds to its own, different cost.
	branches := tree.Branches()
	if len(branches) != 2 {
		t.Fatalf("Branches = %d, want 2", len(branches))
	}
	armB, _ := LiftGroveEntries(tree, branches[0])
	if armB.Cost.InputTokens != 600 { // seed 100 + arm B 500
		t.Errorf("arm B InputTokens = %d, want 600", armB.Cost.InputTokens)
	}

	// And the two arms must genuinely differ, or the fold is not per-arm at all.
	if armB.Cost.InputTokens == meta.Cost.InputTokens {
		t.Error("both arms folded to the same cost; the fold is not path-scoped")
	}

	// The whole-file sum (what aglogs usage reports) is strictly larger than
	// either arm, because it counts every billed line including abandoned ones:
	// seed 100 + arm B 500 + arm C 700.
	const wholeFile int64 = 100 + 500 + 700
	if meta.Cost.InputTokens >= wholeFile {
		t.Errorf("active arm cost %d is not less than the whole-file sum %d; the "+
			"arm fold is not distinct from aglogs usage", meta.Cost.InputTokens, wholeFile)
	}
}

// --- helpers --------------------------------------------------------------

func parseInline(t *testing.T, jsonl string) *transcript.PiSessionTree {
	t.Helper()
	tree, err := transcript.ParsePiSessionTree(strings.NewReader(jsonl))
	if err != nil {
		t.Fatalf("ParsePiSessionTree: %v", err)
	}
	if tree == nil {
		t.Fatal("tree is nil")
	}
	return tree
}

// --- D8 grammar (the shared function) --------------------------------------

func TestValidateComponentMetricKey(t *testing.T) {
	valid := []string{
		"context.off_bundle_reads",
		"skills.load_order_violations",
		"prompt.guard_blocks",
		"a.b",
		"c1.m2_x",
	}
	for _, key := range valid {
		if err := ValidateComponentMetricKey(key); err != nil {
			t.Errorf("ValidateComponentMetricKey(%q) = %v, want nil", key, err)
		}
	}

	invalid := []string{
		"",
		"nodots",
		"too.many.segments",
		"context.BadCase",
		"Context.ok",
		".leading",
		"trailing.",
		"context.with-dash",
		"9bad.key",
		"context. space",
	}
	for _, key := range invalid {
		if err := ValidateComponentMetricKey(key); err == nil {
			t.Errorf("ValidateComponentMetricKey(%q) = nil, want an error", key)
		}
	}
}

// --- P6-17 / Ruling B: the metric that must stay NOT-MEASURED --------------

// THE D4 GUARD. With no bundle manifest there is no denominator, so the key
// must be ABSENT — never 0, never an empty list.
//
// A 0 here would assert "no off-bundle reads occurred", which is a claim about
// the world that nothing measured. That is exactly the defect the shipped
// extension committed: its off_bundle_reads read `args` off an event type with
// no `args` field, so it could never fire and reported "none" forever.
//
// Mandatory mutation: make ComputeOffBundleReads return 0 instead of nil when
// there is no bundle -> this test must FAIL.
func TestOffBundleReadsIsAbsentNotZeroWithoutAManifest(t *testing.T) {
	tree, meta, _ := liftFixture(t, "grove_nested")
	branch := tree.ActivePath()

	// Nothing writes grove-config.json, so no manifest is ever stamped.
	if meta.SeedBundlePaths != nil {
		t.Fatalf("fixture unexpectedly carries a bundle manifest: %v", meta.SeedBundlePaths)
	}

	if got := ComputeOffBundleReads(tree, branch, meta.SeedBundlePaths, "/repo"); got != nil {
		t.Errorf("ComputeOffBundleReads = %v, want nil (not measured). A %v here "+
			"asserts 'no off-bundle reads occurred', which nothing measured.", *got, *got)
	}

	// And it must not reach the emitted map under a made-up key either.
	metrics := ArmComponentMetrics(tree, branch, PiGroveMeta{}, "/repo")
	if _, present := metrics[MetricContextOffBundleReads]; present {
		t.Errorf("%s present in ComponentMetrics with no manifest; it must be absent",
			MetricContextOffBundleReads)
	}
}

// The comparison logic itself is implemented and tested — it is only the
// DENOMINATOR that is missing. This pins the fold against a fixture-supplied
// bundle list so the logic is ready when a seeder finally exists.
//
// Mandatory mutation: invert the bundle-membership test -> the count changes and
// this must FAIL.
//
// The `edit` control is load-bearing and must not be removed. It carries a
// `path` the bundle does NOT contain, which is the only thing that makes the
// tool-NAME filter observable: `bash` alone cannot do it, because bash has no
// `path` key, so firstStringValue returns "" and the call self-filters on the
// very next line whether or not the name filter exists. With bash as the sole
// non-read control, deleting `if strings.ToLower(call.Name) != "read"` left
// this test PASSING — the filter was mutation-provably dead.
//
// Second mandatory mutation: replace the name check with a never-matching
// condition (counting every tool call) -> this must FAIL with 2, want 1.
//
// The two path-less `read` controls (t6, t7) are load-bearing and must not be
// removed. They are the mirror of the `edit` control above, and they exist
// because fixing one guard unpinned the other: once `edit` pinned the NAME
// filter, every remaining `read` in this fixture carried a usable `path`, so
// `if target == "" { continue }` had no control left and was itself
// mutation-provably dead (deleting it left the whole agentlogs suite green).
// t6 omits the `path` key entirely and t7 stamps it empty — the two distinct
// ways firstStringValue returns "" (missing key, and `ok && v != ""`).
//
// Third mandatory mutation: delete `if target == "" { continue }` -> this must
// FAIL with 3, want 1. The three mutation signals are deliberately distinct
// (2 = name filter dead, 3 = path guard dead) so a failure names its own cause.
func TestOffBundleReadsCountsAgainstASuppliedBundle(t *testing.T) {
	tree := parseInline(t, `{"type":"session","version":3,"id":"0198c2f4-0000-7abc-8def-0000000000bb","timestamp":"2026-07-01T10:00:00.000Z"}
{"type":"message","id":"y1","parentId":null,"timestamp":"2026-07-01T10:00:01.000Z","message":{"role":"user","content":"go"}}
{"type":"message","id":"y2","parentId":"y1","timestamp":"2026-07-01T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"t1","name":"read","arguments":{"path":"pkg/in_bundle.go"}},{"type":"toolCall","id":"t2","name":"read","arguments":{"path":"/repo/pkg/also_in_bundle.go"}},{"type":"toolCall","id":"t3","name":"read","arguments":{"path":"pkg/OFF_bundle.go"}},{"type":"toolCall","id":"t4","name":"bash","arguments":{"command":"ls"}},{"type":"toolCall","id":"t5","name":"edit","arguments":{"path":"pkg/OFF_bundle_2.go"}},{"type":"toolCall","id":"t6","name":"read","arguments":{"offset":0}},{"type":"toolCall","id":"t7","name":"read","arguments":{"path":""}}],"model":"m","stopReason":"stop"}}`)

	bundle := []string{"pkg/in_bundle.go", "pkg/also_in_bundle.go"}
	got := ComputeOffBundleReads(tree, tree.ActivePath(), bundle, "/repo")

	if got == nil {
		t.Fatal("got nil with a bundle supplied; a real manifest IS measurable")
	}
	// Only pkg/OFF_bundle.go counts. The absolute read normalises to a bundled
	// path; bash is not a read AND carries no path; `edit` is not a read but
	// DOES carry an off-bundle path, so it is discriminated by name alone; t6
	// and t7 ARE reads but carry no usable path, so they are discriminated by
	// the empty-path guard alone.
	if *got != 1 {
		t.Errorf("ComputeOffBundleReads = %v, want 1. A 2 here means the tool-name "+
			"filter is not applied and every path-bearing tool call is being counted "+
			"as a read. A 3 means the empty-path guard is not applied and reads with "+
			"no usable path are being counted as off-bundle — a fabricated "+
			"measurement, since an unknown path cannot be known to be off-bundle.", *got)
	}
}

// An empty-but-present manifest is a real measurement, distinct from nil: it
// declares that the bundle contained nothing (a cold-start arm), so every read
// is genuinely off-bundle.
//
// THIS TEST DRIVES THE VALUE THROUGH THE LIFT, and that is the point. An
// earlier version passed a hand-built []string{} straight to
// ComputeOffBundleReads — a state meta.SeedBundlePaths could never actually
// hold, because liftNestedConfig tested `len(data.BundleFiles) > 0` and so
// collapsed a stamped "bundle_files": [] to nil. The two files encoded OPPOSITE
// semantics for the same value and the suite asserted both without noticing,
// because the assertion guarded an unreachable branch.
//
// Mandatory mutation: revert the lift to `len(data.BundleFiles) > 0` -> this
// must FAIL (the empty manifest arrives as nil = not-measured).
func TestOffBundleReadsEmptyManifestIsMeasured(t *testing.T) {
	tree, meta, _ := liftFixture(t, "grove_empty_bundle")

	// The lift must preserve declared-empty as empty-NON-NIL. If this is nil the
	// manifest's measurement was discarded on the way in and everything below is
	// vacuous.
	if meta.SeedBundlePaths == nil {
		t.Fatal("the lift collapsed a stamped \"bundle_files\": [] to nil. " +
			"An empty manifest is a REAL measurement (every read is off-bundle); " +
			"only an ABSENT key means not-measured (D4).")
	}
	if len(meta.SeedBundlePaths) != 0 {
		t.Fatalf("fixture should declare an empty bundle, got %v", meta.SeedBundlePaths)
	}

	got := ComputeOffBundleReads(tree, tree.ActivePath(), meta.SeedBundlePaths, "/repo")
	if got == nil {
		t.Fatal("an empty manifest is present and therefore measured; want non-nil")
	}
	if *got != 1 {
		t.Errorf("= %v, want 1", *got)
	}

	// And it must reach the emitted map as a real measured value — including a
	// measured ZERO, which is the mirror-image D4 error to a fabricated one.
	metrics := ArmComponentMetrics(tree, tree.ActivePath(), meta, "/repo")
	if _, present := metrics[MetricContextOffBundleReads]; !present {
		t.Errorf("%s absent despite a present (empty) manifest; a real measurement "+
			"was discarded as not-measured", MetricContextOffBundleReads)
	}
}

// Wave one's honest result: the pi lift contributes NO component metric keys.
// If this test ever starts failing because a key appeared, that key needs to be
// justified — not accommodated.
func TestWaveOneShipsNoComputedComponentMetrics(t *testing.T) {
	tree, meta, _ := liftFixture(t, "grove_arm_seed_then_variant")

	// meta carries no grove_metric entry in this fixture, and neither post-hoc
	// fold can produce a key.
	got := ArmComponentMetrics(tree, tree.ActivePath(), meta, "/repo")
	if len(got) != 0 {
		t.Errorf("ComponentMetrics = %v, want empty. Wave one ships zero computed "+
			"metrics: off_bundle_reads has no denominator and load_order_violations "+
			"has no ordering source.", got)
	}
}

// --- P6-22: the D2 round-trip, emitter -> lifter --------------------------

// THE MIRROR CHECK. grove_emitted.jsonl is generated by the REAL TypeScript
// emitter (agent/tools/emit-fixtures.mjs -> make fixtures), not hand-written, so
// this is the only thing holding the hand-written TS ConfigVector mirror to the
// Go struct.
//
// If the TS canonical serializer drifts from Go's encoding/json in ANY respect —
// field order, omitempty handling, sorted map keys, HTML escaping of <>& — the
// recomputed hash diverges from the emitted one and the lift raises
// config_hash_mismatch. That warning firing here is a REAL FAILURE of the
// emitter, not a test to relax.
func TestEmittedFixtureRoundTripsThroughTheLifter(t *testing.T) {
	tree, meta, warnings := liftFixture(t, "grove_emitted")

	for _, w := range warnings {
		if w.Code == WarnConfigHashMismatch {
			t.Fatalf("THE TS EMITTER HAS DRIFTED FROM record.ConfigVector: %s\n"+
				"Fix agent/package/extensions/config-vector.ts (or stop emitting "+
				"config_hash) — do not weaken this assertion.", w.Message)
		}
		if w.Code == WarnLegacyConfigShape {
			t.Errorf("the emitter is still writing the legacy flat shape: %s", w.Message)
		}
	}

	if !meta.Attributed() {
		t.Fatal("emitted fixture produced no Config")
	}
	// Independently recompute rather than trusting the absence of a warning.
	if meta.ConfigHash != meta.Config.Hash() {
		t.Errorf("emitted config_hash %q != Go ConfigVector.Hash() %q",
			meta.ConfigHash, meta.Config.Hash())
	}

	// The emitter's variant stamp must win over the copied seed stamp.
	if got := meta.Config.Components["context"]; got != "c-VARIANT" {
		t.Errorf("components[context] = %q, want c-VARIANT", got)
	}
	if meta.TaskID != "t-042" {
		t.Errorf("TaskID = %q, want t-042", meta.TaskID)
	}
	if meta.Replicate == nil || *meta.Replicate != 0 {
		t.Errorf("Replicate = %v, want a stamped 0", meta.Replicate)
	}
	if !meta.Sweep {
		t.Error("Sweep = false, want true")
	}
	if meta.Schema != "0.2.0" {
		t.Errorf("Schema = %q, want 0.2.0", meta.Schema)
	}
	if meta.Config.WorktreeCommit == "" {
		t.Error("WorktreeCommit did not survive the round trip")
	}

	// P6-20: the emitter must no longer produce off_bundle_reads at all.
	if _, present := meta.Metrics[MetricContextOffBundleReads]; present {
		t.Errorf("emitted grove_metric still carries %s; it was deleted because it "+
			"could never fire and had no denominator", MetricContextOffBundleReads)
	}
	// turns/tool_counts are diagnostics and must NOT arrive as D8 metric keys.
	if len(meta.Metrics) != 0 {
		t.Errorf("Metrics = %v, want empty — turns/tool_counts are diagnostics, "+
			"not ComponentMetrics axes", meta.Metrics)
	}

	_ = tree
}
