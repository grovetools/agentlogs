package transcript

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// updateGolden regenerates the committed golden files. It exists so the goldens
// could be captured from the PRE-refactor NormalizePiFile: capturing them after
// a refactor would only prove the refactor agrees with itself.
//
//	go test ./pkg/transcript/ -run TestNormalizePiFileGolden -update-pi-golden
var updateGolden = flag.Bool("update-pi-golden", false, "rewrite pi golden files")

const piTreeDir = "testdata/pi/trees"

// piTreeFixtures is every committed tree fixture.
//
// PROVENANCE — these are HAND-AUTHORED, not observed. Every real pi transcript
// on the authoring host is provider "openai-codex", short (median 9 entries),
// and exhibits zero branching, zero compaction and zero custom_message entries.
// Branching and compaction are documented and implemented in pi but are not
// exercised by any sample available here, so the fork/compaction/cycle/orphan
// fixtures were written against pi's own docs/session-format.md (CompactionEntry
// and BranchSummaryEntry shapes) and session-manager.ts, NOT copied from a real
// session. Treat them as a faithful reading of the format, not as ground truth.
//
// The grove_* fixtures' config hashes ARE real: they were produced by running
// record.ConfigVector.Hash() over the embedded vectors rather than invented.
var piTreeFixtures = []string{
	"linear",
	"fork_two_arm",
	"orphan_root",
	"cycle",
	"torn_line",
	"multi_model",
	"compaction",
	"grove_nested",
	"grove_legacy_flat",
	"grove_hash_mismatch",
	"grove_bad_metric_key",
	"grove_arm_seed_then_variant",
	"grove_unattributed",
	"grove_emitted",
	"grove_empty_bundle",
}

func fixturePath(name string) string {
	return filepath.Join(piTreeDir, name+".jsonl")
}

func goldenPath(name string) string {
	return filepath.Join(piTreeDir, "golden", name+".json")
}

// TestNormalizePiFileGolden pins NormalizePiFile's output on every fixture.
//
// This is the load-bearing regression test for the P6-07 refactor
// (NormalizePiFile reimplemented on top of ParsePiSessionTree). The goldens were
// captured from the ORIGINAL implementation before any of that code existed, so
// a mismatch means the refactor changed rendered output — which it must not,
// under any fixture, ever.
func TestNormalizePiFileGolden(t *testing.T) {
	for _, name := range piTreeFixtures {
		t.Run(name, func(t *testing.T) {
			f, err := os.Open(fixturePath(name))
			if err != nil {
				t.Fatalf("open fixture: %v", err)
			}
			defer f.Close()

			entries, err := NormalizePiFile(f)
			if err != nil {
				t.Fatalf("NormalizePiFile: %v", err)
			}

			got, err := json.MarshalIndent(entries, "", "  ")
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got = append(got, '\n')

			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(goldenPath(name)), 0o755); err != nil {
					t.Fatalf("mkdir golden: %v", err)
				}
				if err := os.WriteFile(goldenPath(name), got, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}

			want, err := os.ReadFile(goldenPath(name))
			if err != nil {
				t.Fatalf("read golden (run with -update-pi-golden to create): %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("NormalizePiFile output changed for %s.\n--- got ---\n%s\n--- want ---\n%s",
					name, got, want)
			}
		})
	}
}

// --- tree structure -------------------------------------------------------

// parseInlineTree parses a JSONL literal, for shapes no committed fixture
// carries (deliberately malformed or adversarial entries).
func parseInlineTree(t *testing.T, jsonl string) *PiSessionTree {
	t.Helper()
	tree, err := ParsePiSessionTree(strings.NewReader(jsonl))
	if err != nil {
		t.Fatalf("ParsePiSessionTree: %v", err)
	}
	if tree == nil {
		t.Fatal("tree is nil")
	}
	return tree
}

func parseFixture(t *testing.T, name string) *PiSessionTree {
	t.Helper()
	f, err := os.Open(fixturePath(name))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	tree, err := ParsePiSessionTree(f)
	if err != nil {
		t.Fatalf("ParsePiSessionTree: %v", err)
	}
	if tree == nil {
		t.Fatal("tree is nil")
	}
	return tree
}

func TestParsePiSessionTreeLinearHasOneBranch(t *testing.T) {
	tree := parseFixture(t, "linear")

	branches := tree.Branches()
	if len(branches) != 1 {
		t.Fatalf("Branches() = %d, want 1 for a linear session", len(branches))
	}
	if got := branches[0].ForkPointID; got != "" {
		t.Errorf("ForkPointID = %q, want empty for an unforked path", got)
	}
	want := []string{"a1", "a2", "a3", "a4"}
	assertIDs(t, "linear path", branches[0].PathIDs, want)

	// On a linear file the sole branch IS the active path.
	assertIDs(t, "active path", tree.ActivePath().PathIDs, want)
}

func TestParsePiSessionTreeEnumeratesBothArms(t *testing.T) {
	tree := parseFixture(t, "fork_two_arm")

	branches := tree.Branches()
	if len(branches) != 2 {
		t.Fatalf("Branches() = %d, want 2", len(branches))
	}

	// File order: the abandoned arm's leaf (b4) precedes the active one (c4).
	assertIDs(t, "arm B", branches[0].PathIDs, []string{"a1", "a2", "b3", "b4"})
	assertIDs(t, "arm C", branches[1].PathIDs, []string{"a1", "a2", "c3", "c4"})

	for _, b := range branches {
		if b.ForkPointID != "a2" {
			t.Errorf("ForkPointID = %q, want a2", b.ForkPointID)
		}
	}

	// The abandoned arm must NOT be the active path — that is the whole reason
	// NormalizePiFile linearizes rather than reading the file in order.
	assertIDs(t, "active path", tree.ActivePath().PathIDs, []string{"a1", "a2", "c3", "c4"})
}

func TestArmsReturnsSuffixesFromForkPoint(t *testing.T) {
	tree := parseFixture(t, "fork_two_arm")

	arms := tree.Arms("a2")
	if len(arms) != 2 {
		t.Fatalf("Arms(a2) = %d, want 2", len(arms))
	}
	// Suffixes begin AT the fork point, not at the tree root.
	assertIDs(t, "arm B suffix", arms[0].PathIDs, []string{"a2", "b3", "b4"})
	assertIDs(t, "arm C suffix", arms[1].PathIDs, []string{"a2", "c3", "c4"})

	if got := tree.Arms("nonexistent"); got != nil {
		t.Errorf("Arms(nonexistent) = %v, want nil", got)
	}
}

// A dangling parentId makes the entry a root rather than an error, matching the
// original walk's tolerance (a missing parent simply terminated it).
func TestParsePiSessionTreeTreatsOrphansAsRoots(t *testing.T) {
	tree := parseFixture(t, "orphan_root")

	if len(tree.roots) != 2 {
		t.Fatalf("roots = %v, want 2 (a1 and the orphan d1)", tree.roots)
	}
	assertIDs(t, "roots", tree.roots, []string{"a1", "d1"})
	// d2 is the last line, so the orphan chain is the active path.
	assertIDs(t, "active path", tree.ActivePath().PathIDs, []string{"d1", "d2"})
}

// A parent cycle must truncate the walk, never hang or error.
func TestParsePiSessionTreeCycleTerminates(t *testing.T) {
	tree := parseFixture(t, "cycle")

	path := tree.ActivePath().PathIDs
	if len(path) != 2 {
		t.Fatalf("cycle path = %v, want 2 entries before the guard trips", path)
	}
	seen := map[string]bool{}
	for _, id := range path {
		if seen[id] {
			t.Errorf("id %q repeated in path %v", id, path)
		}
		seen[id] = true
	}
}

// A torn trailing line is dropped, and the entries before it still parse — a
// live session file is written incrementally and can be read mid-append.
func TestParsePiSessionTreeToleratesTornTrailingLine(t *testing.T) {
	tree := parseFixture(t, "torn_line")

	assertIDs(t, "active path", tree.ActivePath().PathIDs, []string{"a1", "a2"})
	if _, ok := tree.byID["a3"]; ok {
		t.Error("torn entry a3 was admitted to the tree")
	}
}

// An empty or header-only file is a legitimate empty transcript, not an error.
func TestParsePiSessionTreeEmptyInputIsNotAnError(t *testing.T) {
	for _, in := range []string{
		"",
		"\n\n",
		`{"type":"session","version":3,"id":"0198c2f4-0000-7abc-8def-00000000ffff","timestamp":"2026-07-01T10:00:00.000Z"}`,
	} {
		tree, err := ParsePiSessionTree(strings.NewReader(in))
		if err != nil {
			t.Errorf("ParsePiSessionTree(%q): unexpected error %v", in, err)
		}
		if tree != nil {
			t.Errorf("ParsePiSessionTree(%q) = %v, want nil tree", in, tree)
		}
	}
}

// custom entries must be present in the TREE (the lift reads them) while
// remaining invisible to the rendered transcript.
func TestCustomEntriesAreInTreeButNotRendered(t *testing.T) {
	tree := parseFixture(t, "grove_nested")

	raw, ok := tree.byID["g1"]
	if !ok {
		t.Fatal("custom entry g1 missing from the tree")
	}
	if raw.CustomType != "grove_config" {
		t.Errorf("CustomType = %q, want grove_config", raw.CustomType)
	}
	if len(raw.Data) == 0 {
		t.Error("custom entry Data is empty — the discriminator fields did not parse")
	}

	for _, entry := range tree.Normalize(tree.ActivePath()) {
		if entry.MessageID == "g1" || entry.MessageID == "g4" {
			t.Errorf("custom entry %q leaked into the rendered transcript", entry.MessageID)
		}
	}
}

// CustomEntriesOn selects on the ENTRY TYPE, not on customType.
//
// The fixture deliberately puts a stray "customType":"grove_config" on a
// `message` entry. That is the only control that can tell the type predicate
// apart from its downstream masker: LiftGroveEntries switches on CustomType and
// ignores anything it does not recognise, so every ordinary fixture entry (with
// an EMPTY customType) is discarded downstream whether or not the type check
// runs. Only an entry that WOULD survive the downstream switch proves the type
// check is doing the work.
//
// Mandatory mutation: drop `if raw.Type != "custom" { continue }` -> this must
// FAIL with 2 entries instead of 1.
func TestCustomEntriesOnSelectsByEntryTypeNotCustomType(t *testing.T) {
	tree := parseInlineTree(t, `{"type":"session","version":3,"id":"0198c2f4-0000-7abc-8def-0000000000d1","timestamp":"2026-07-01T10:00:00.000Z"}
{"type":"custom","id":"c1","parentId":null,"timestamp":"2026-07-01T10:00:01.000Z","customType":"grove_config","data":{"config":null}}
{"type":"message","id":"m1","parentId":"c1","timestamp":"2026-07-01T10:00:02.000Z","customType":"grove_config","message":{"role":"user","content":"a message wearing a customType"}}`)

	got := tree.CustomEntriesOn(tree.ActivePath())
	if len(got) != 1 {
		ids := make([]string, 0, len(got))
		for _, e := range got {
			ids = append(ids, e.ID)
		}
		t.Fatalf("CustomEntriesOn returned %d entries %v, want 1 (only c1). A `message` "+
			"entry carrying a customType is NOT a custom entry; admitting it would feed "+
			"the lift a config stamp from a real conversation turn.", len(got), ids)
	}
	if got[0].ID != "c1" {
		t.Errorf("got entry %q, want c1", got[0].ID)
	}
}

// ModelsOn selects on the ENTRY TYPE too.
//
// The control is a `compaction` entry that DOES carry a message block naming a
// model. An ordinary compaction fixture entry has no message field at all, so
// the `len(raw.Message) == 0` guard masks the type check; this one survives that
// guard and so isolates it.
//
// Relevant because LiftGroveEntries raises WarnModelDisagreement off this set:
// a phantom second model would report an arm as model-inconsistent when it is
// not.
//
// Mandatory mutation: drop `raw.Type != "message"` from the guard -> this must
// FAIL with two models.
func TestModelsOnIgnoresNonMessageEntriesCarryingAMessage(t *testing.T) {
	tree := parseInlineTree(t, `{"type":"session","version":3,"id":"0198c2f4-0000-7abc-8def-0000000000d2","timestamp":"2026-07-01T10:00:00.000Z"}
{"type":"message","id":"m1","parentId":null,"timestamp":"2026-07-01T10:00:01.000Z","message":{"role":"assistant","content":[{"type":"text","text":"hi"}],"model":"real-model","stopReason":"stop"}}
{"type":"compaction","id":"k1","parentId":"m1","timestamp":"2026-07-01T10:00:02.000Z","summary":"squashed","message":{"role":"assistant","content":[{"type":"text","text":"x"}],"model":"phantom-model"}}`)

	got := tree.ModelsOn(tree.ActivePath())
	if len(got) != 1 || got[0] != "real-model" {
		t.Errorf("ModelsOn = %v, want [real-model]. A compaction entry's embedded "+
			"message is not an assistant turn; counting it invents a second model and "+
			"would raise a spurious model_disagreement warning.", got)
	}
}

func assertIDs(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s[%d] = %q, want %q (full: %v)", what, i, got[i], want[i], got)
		}
	}
}

// The fixture set must actually exercise the shapes it claims to. A fixture
// that silently stopped containing a fork would make the fork tests vacuous.
func TestPiTreeFixturesCoverClaimedShapes(t *testing.T) {
	checks := map[string]string{
		"fork_two_arm":                `"id":"c3"`,
		"orphan_root":                 `"parentId":"missing-parent"`,
		"cycle":                       `"id":"e2","parentId":"e1"`,
		"compaction":                  `"type":"compaction"`,
		"grove_nested":                `"customType":"grove_config"`,
		"grove_legacy_flat":           `"grove_metrics_version":"0.1.0"`,
		"grove_arm_seed_then_variant": `"sweep":true`,
	}
	for name, needle := range checks {
		b, err := os.ReadFile(fixturePath(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(b), needle) {
			t.Errorf("fixture %s no longer contains %s", name, needle)
		}
	}
}
