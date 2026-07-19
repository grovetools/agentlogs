package metrics

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/grovetools/agentlogs/pkg/transcript"
	"github.com/grovetools/eval/pkg/record"
)

// --- helpers -------------------------------------------------------------

// iv/fv dereference the D4 "nil = not measured" pointers for assertions,
// mapping nil to a sentinel that can never equal a legitimate count so a
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

func textPart(s string) transcript.UnifiedPart {
	return transcript.UnifiedPart{Type: PartTypeText, Content: transcript.UnifiedTextContent{Text: s}}
}

func toolPart(name string, input map[string]interface{}) transcript.UnifiedPart {
	return transcript.UnifiedPart{
		Type:    PartTypeToolCall,
		Content: transcript.UnifiedToolCall{ID: name, Name: name, Input: input},
	}
}

func resultPart(id, out string) transcript.UnifiedPart {
	return transcript.UnifiedPart{
		Type:    PartTypeToolResult,
		Content: transcript.UnifiedToolResult{ToolCallID: id, Output: out},
	}
}

func deref(t *testing.T, p *int, what string) int {
	t.Helper()
	if p == nil {
		t.Fatalf("%s: expected a measured value, got nil", what)
	}
	return *p
}

// --- P1-20: the claude file-touch table ----------------------------------

// The claude table has no transcript fixture upstream, so it is pinned here
// against a hand-built entry set.
func TestComputeClaudeFileToolTable(t *testing.T) {
	entries := []transcript.UnifiedEntry{
		{Role: "user", Provider: "claude", Parts: []transcript.UnifiedPart{textPart("go")}},
		{Role: "assistant", Provider: "claude", Parts: []transcript.UnifiedPart{
			toolPart("Read", map[string]interface{}{"file_path": "/repo/a.go"}),
			toolPart("Grep", map[string]interface{}{"pattern": "x", "path": "/repo/pkg"}),
			toolPart("Edit", map[string]interface{}{"file_path": "/repo/a.go"}),
			toolPart("Write", map[string]interface{}{"file_path": "/repo/b.go"}),
			toolPart("MultiEdit", map[string]interface{}{"file_path": "/repo/c.go"}),
			toolPart("NotebookEdit", map[string]interface{}{"notebook_path": "/repo/nb.ipynb"}),
		}},
	}

	got := Compute(entries)

	if got.Provider != "claude" {
		t.Errorf("Provider = %q, want claude", got.Provider)
	}
	// Read+Grep+Edit+Write+MultiEdit+NotebookEdit
	if iv(got.ToolCalls) != 6 {
		t.Errorf("ToolCalls = %d, want 6", iv(got.ToolCalls))
	}
	if iv(got.DistinctTools) != 6 {
		t.Errorf("DistinctTools = %d, want 6", iv(got.DistinctTools))
	}

	// /repo/a.go (Read+Edit, deduped), /repo/pkg, /repo/b.go, /repo/c.go, /repo/nb.ipynb
	if n := deref(t, got.FilesTouched, "FilesTouched"); n != 5 {
		t.Errorf("FilesTouched = %d, want 5 (got %v)", n, got.TouchedFiles)
	}
	// a.go, b.go, c.go, nb.ipynb — Read and Grep are not edits.
	if n := deref(t, got.FilesEdited, "FilesEdited"); n != 4 {
		t.Errorf("FilesEdited = %d, want 4 (got %v)", n, got.EditedFiles)
	}
	if len(got.Unsupported) != 0 {
		t.Errorf("Unsupported = %v, want empty for claude", got.Unsupported)
	}
	// ForbiddenTouches is eval's job, never agentlogs'.
	if got.ForbiddenTouches != nil {
		t.Errorf("ForbiddenTouches = %v, want nil (requires the fixture manifest)", *got.ForbiddenTouches)
	}
}

// Unknown tools contribute nothing rather than guessing. An agent that only
// ran Bash has measured-zero files, which is different from unmeasurable.
func TestComputeUnknownToolsContributeNothing(t *testing.T) {
	entries := []transcript.UnifiedEntry{
		{Role: "assistant", Provider: "claude", Parts: []transcript.UnifiedPart{
			toolPart("Bash", map[string]interface{}{"command": "rm -rf /repo/a.go"}),
			toolPart("WebFetch", map[string]interface{}{"url": "https://example.com"}),
			toolPart("TodoWrite", map[string]interface{}{"todos": []interface{}{}}),
			// THE PROVIDER CONTROL — do not remove.
			//
			// `read` (lowercase) is pi's row, not claude's, and it is keyed on
			// "path" where claude's Read is keyed on "file_path". This entry is
			// claude, so the PROVIDER predicate in fileTouches.observe must
			// reject it.
			//
			// Every other non-matching call above is keyed on command/url/todos,
			// which the later empty-path predicate rejects anyway — so without
			// THIS row, deleting the provider check from the rule match changes
			// nothing and the provider column of fileToolTable is unpinned. This
			// row is the only one that tells the two predicates apart.
			toolPart("read", map[string]interface{}{"path": "/repo/pi_keyed.go"}),
		}},
	}

	got := Compute(entries)

	if iv(got.ToolCalls) != 4 {
		t.Errorf("ToolCalls = %d, want 4", iv(got.ToolCalls))
	}
	if n := deref(t, got.FilesTouched, "FilesTouched"); n != 0 {
		t.Errorf("FilesTouched = %d, want 0 (measured zero, not nil); touched=%v. "+
			"A 1 here means a pi rule matched a claude entry — the provider column "+
			"of fileToolTable is not being applied.", n, got.TouchedFiles)
	}
	if len(got.Unsupported) != 0 {
		t.Errorf("Unsupported = %v, want empty: claude IS measurable, it just touched nothing", got.Unsupported)
	}
}

// Rules whose input key is absent or of the wrong type contribute nothing.
func TestComputeMalformedInputContributesNothing(t *testing.T) {
	entries := []transcript.UnifiedEntry{
		{Role: "assistant", Provider: "claude", Parts: []transcript.UnifiedPart{
			toolPart("Read", map[string]interface{}{"file_path": 42}),
			toolPart("Edit", map[string]interface{}{"file_path": ""}),
			toolPart("Write", nil),
			toolPart("Read", map[string]interface{}{"wrong_key": "/repo/a.go"}),
		}},
	}

	got := Compute(entries)

	if iv(got.ToolCalls) != 4 {
		t.Errorf("ToolCalls = %d, want 4", iv(got.ToolCalls))
	}
	if n := deref(t, got.FilesTouched, "FilesTouched"); n != 0 {
		t.Errorf("FilesTouched = %d, want 0; touched=%v", n, got.TouchedFiles)
	}
}

// --- P6-15: pi is measurable ---------------------------------------------

// pi's file-taking tools (read/grep/find/ls/edit/write) all spell the argument
// "path"; bash takes {command, timeout} and contributes nothing. Previously pi
// was declared unsupported, so these counts came back nil — "cannot measure"
// for something that is plainly measurable, which is a D4 error in the
// optimistic direction.
func TestComputePiCountsPathKeyedTools(t *testing.T) {
	entries := []transcript.UnifiedEntry{
		{Role: "user", Provider: "pi", Parts: []transcript.UnifiedPart{textPart("go")}},
		{Role: "assistant", Provider: "pi", Parts: []transcript.UnifiedPart{
			// Every tool below reads a path NO other tool touches. Sharing a
			// path between read and edit would let the edit row cover for a
			// missing read row, and the mutation "drop the read row" would pass
			// — which is exactly what happened on the first draft of this test.
			toolPart("read", map[string]interface{}{"path": "/repo/only-read.go"}),
			toolPart("edit", map[string]interface{}{"path": "/repo/util.go"}),
			toolPart("write", map[string]interface{}{"path": "/repo/new.go"}),
			toolPart("grep", map[string]interface{}{"pattern": "func", "path": "/repo/pkg"}),
			toolPart("ls", map[string]interface{}{"path": "/repo"}),
			toolPart("find", map[string]interface{}{"pattern": "*.go", "path": "/repo/cmd"}),
			toolPart("bash", map[string]interface{}{"command": "go test ./..."}),
		}},
	}

	got := Compute(entries)

	if len(got.Unsupported) != 0 {
		t.Errorf("Unsupported = %v, want empty", got.Unsupported)
	}
	wantTouched := []string{"/repo", "/repo/cmd", "/repo/new.go", "/repo/only-read.go", "/repo/pkg", "/repo/util.go"}
	wantEdited := []string{"/repo/new.go", "/repo/util.go"}

	if n := deref(t, got.FilesTouched, "FilesTouched"); n != len(wantTouched) {
		t.Errorf("FilesTouched = %d, want %d; touched=%v", n, len(wantTouched), got.TouchedFiles)
	}
	if n := deref(t, got.FilesEdited, "FilesEdited"); n != len(wantEdited) {
		t.Errorf("FilesEdited = %d, want %d; edited=%v", n, len(wantEdited), got.EditedFiles)
	}
	for i, want := range wantTouched {
		if got.TouchedFiles[i] != want {
			t.Errorf("TouchedFiles[%d] = %q, want %q", i, got.TouchedFiles[i], want)
		}
	}
	for i, want := range wantEdited {
		if got.EditedFiles[i] != want {
			t.Errorf("EditedFiles[%d] = %q, want %q", i, got.EditedFiles[i], want)
		}
	}
	// bash's shell string is not a path and must not be counted as one.
	for _, f := range got.TouchedFiles {
		if strings.Contains(f, "go test") {
			t.Errorf("bash command leaked into touched files: %q", f)
		}
	}
}

// --- P1-20: the unsupported-provider ruling ------------------------------

func TestComputeUnsupportedProviders(t *testing.T) {
	// Each of these providers exposes file work only through opaque shell
	// strings or unnameable tools; see providerSupported's doc comment.
	//
	// pi was on this list and has been REMOVED: its six file-taking tools all
	// key on "path", so it is genuinely measurable and now has rows in
	// fileToolTable. See TestComputePiCountsPathKeyedTools below.
	for _, provider := range []string{"codex", "opencode"} {
		t.Run(provider, func(t *testing.T) {
			entries := []transcript.UnifiedEntry{
				{Role: "user", Provider: provider, Parts: []transcript.UnifiedPart{textPart("go")}},
				{Role: "assistant", Provider: provider, Parts: []transcript.UnifiedPart{
					toolPart("shell", map[string]interface{}{
						"command": []interface{}{"bash", "-lc", "ls *.go"},
					}),
				}},
			}

			got := Compute(entries)

			if got.FilesTouched != nil {
				t.Errorf("FilesTouched = %d, want nil for %s", *got.FilesTouched, provider)
			}
			if got.FilesEdited != nil {
				t.Errorf("FilesEdited = %d, want nil for %s", *got.FilesEdited, provider)
			}
			want := []string{UnsupportedFilesTouched, UnsupportedFilesEdited}
			if len(got.Unsupported) != len(want) {
				t.Fatalf("Unsupported = %v, want %v", got.Unsupported, want)
			}
			for i := range want {
				if got.Unsupported[i] != want[i] {
					t.Errorf("Unsupported[%d] = %q, want %q", i, got.Unsupported[i], want[i])
				}
			}
			// Tool calls are still counted for unsupported providers — only
			// the file measurements are withheld.
			if iv(got.ToolCalls) != 1 {
				t.Errorf("ToolCalls = %d, want 1", iv(got.ToolCalls))
			}
		})
	}
}

// nil file counts must vanish from JSON entirely, so a consumer cannot read
// them as zero.
func TestResultJSONOmitsUnmeasuredCounts(t *testing.T) {
	got := Compute([]transcript.UnifiedEntry{
		{Role: "assistant", Provider: "codex", Parts: []transcript.UnifiedPart{
			toolPart("shell", map[string]interface{}{"command": []interface{}{"ls"}}),
		}},
	})

	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, key := range []string{"files_touched", "files_edited", "forbidden_touches"} {
		if _, present := decoded[key]; present {
			t.Errorf("key %q present in JSON, want omitted so it cannot be read as 0", key)
		}
	}
	// The embedded record.Process fields must inline at the top level.
	for _, key := range []string{"tool_calls", "distinct_tools", "turns"} {
		if _, present := decoded[key]; !present {
			t.Errorf("key %q missing; record.Process should inline", key)
		}
	}
	if _, present := decoded["unsupported"]; !present {
		t.Error("unsupported list missing")
	}
}

// --- P1-18: the fold -----------------------------------------------------

// The PROVIDER detection loop skips sidechains too, and that skip needs its own
// control.
//
// TestComputeExcludesSidechainEntries cannot pin it: every entry there declares
// provider "claude", so removing the skip from the provider loop yields the same
// answer either way. Here the sidechain entry comes FIRST and declares a
// different provider, so whichever entry the loop reaches first decides the
// result — which is exactly the distinction the skip makes.
//
// Mandatory mutation: remove `if entry.IsSidechain { continue }` from the
// provider loop -> this must FAIL with "pi".
func TestComputeProviderIgnoresSidechainEntries(t *testing.T) {
	entries := []transcript.UnifiedEntry{
		// A subagent running a different provider, ahead of any main-chain entry.
		{Role: "assistant", Provider: "pi", IsSidechain: true, AgentID: "sub-1",
			Parts: []transcript.UnifiedPart{textPart("subagent work")}},
		{Role: "user", Provider: "claude", Parts: []transcript.UnifiedPart{textPart("main turn")}},
	}

	got := Compute(entries)

	if got.Provider != "claude" {
		t.Errorf("Provider = %q, want claude. The provider is a property of the "+
			"MAIN chain; taking it from a sidechain entry mislabels the whole "+
			"record's Process axis.", got.Provider)
	}
}

func TestComputeExcludesSidechainEntries(t *testing.T) {
	entries := []transcript.UnifiedEntry{
		{Role: "user", Provider: "claude", Parts: []transcript.UnifiedPart{textPart("main turn")}},
		{Role: "assistant", Provider: "claude", Parts: []transcript.UnifiedPart{
			toolPart("Read", map[string]interface{}{"file_path": "/repo/main.go"}),
		}},
		// Everything below is subagent work and must not be counted.
		{Role: "user", Provider: "claude", IsSidechain: true, AgentID: "sub-1",
			Parts: []transcript.UnifiedPart{textPart("sidechain turn")}},
		{Role: "assistant", Provider: "claude", IsSidechain: true, AgentID: "sub-1",
			Parts: []transcript.UnifiedPart{
				toolPart("Grep", map[string]interface{}{"path": "/repo/sub"}),
				toolPart("Write", map[string]interface{}{"file_path": "/repo/sub.go"}),
			}},
	}

	got := Compute(entries)

	if iv(got.ToolCalls) != 1 {
		t.Errorf("ToolCalls = %d, want 1 (sidechain tool calls excluded)", iv(got.ToolCalls))
	}
	if iv(got.DistinctTools) != 1 {
		t.Errorf("DistinctTools = %d, want 1", iv(got.DistinctTools))
	}
	if iv(got.Turns) != 1 {
		t.Errorf("Turns = %d, want 1 (sidechain turn excluded)", iv(got.Turns))
	}
	if n := deref(t, got.FilesTouched, "FilesTouched"); n != 1 {
		t.Errorf("FilesTouched = %d, want 1; touched=%v", n, got.TouchedFiles)
	}
	if n := deref(t, got.FilesEdited, "FilesEdited"); n != 0 {
		t.Errorf("FilesEdited = %d, want 0 (sidechain Write excluded)", n)
	}
}

func TestComputeTurnsCountOnlyTextBearingUserEntries(t *testing.T) {
	entries := []transcript.UnifiedEntry{
		{Role: "user", Provider: "claude", Parts: []transcript.UnifiedPart{textPart("first")}},
		// Pure tool_result carrier: transport, not a turn.
		{Role: "user", Provider: "claude", Parts: []transcript.UnifiedPart{resultPart("t1", "output")}},
		// Whitespace-only text does not make a turn.
		{Role: "user", Provider: "claude", Parts: []transcript.UnifiedPart{textPart("   \n\t ")}},
		// Empty text does not make a turn.
		{Role: "user", Provider: "claude", Parts: []transcript.UnifiedPart{textPart("")}},
		// Mixed: a tool result AND real text does count.
		{Role: "user", Provider: "claude", Parts: []transcript.UnifiedPart{
			resultPart("t2", "output"), textPart("second"),
		}},
		// Assistant text is never a turn.
		{Role: "assistant", Provider: "claude", Parts: []transcript.UnifiedPart{textPart("reply")}},
	}

	got := Compute(entries)

	if iv(got.Turns) != 2 {
		t.Errorf("Turns = %d, want 2", iv(got.Turns))
	}
}

func TestComputeDistinctToolsPreservesCase(t *testing.T) {
	entries := []transcript.UnifiedEntry{
		{Role: "assistant", Provider: "claude", Parts: []transcript.UnifiedPart{
			toolPart("Read", map[string]interface{}{"file_path": "/a"}),
			toolPart("Read", map[string]interface{}{"file_path": "/b"}),
			// Case-preserved for DistinctTools...
			toolPart("read", map[string]interface{}{"file_path": "/c"}),
		}},
	}

	got := Compute(entries)

	if iv(got.ToolCalls) != 3 {
		t.Errorf("ToolCalls = %d, want 3", iv(got.ToolCalls))
	}
	if iv(got.DistinctTools) != 2 {
		t.Errorf("DistinctTools = %d, want 2 (Read and read are distinct names)", iv(got.DistinctTools))
	}
	// ...but the file-touch table matches case-insensitively, so all three
	// paths land.
	if n := deref(t, got.FilesTouched, "FilesTouched"); n != 3 {
		t.Errorf("FilesTouched = %d, want 3; touched=%v", n, got.TouchedFiles)
	}
}

// A tool_call part whose Content is NEITHER a UnifiedToolCall nor a map falls
// through both branches of partToolCall to a zero-value call, whose Name is "".
// That call is still a real tool call and is counted as one, but "" is not a
// tool NAME and must not become an entry in the distinct set.
//
// Without the `call.Name != ""` guard, "" is admitted as a distinct tool and
// DistinctTools reports a tool that does not exist — and it inflates by exactly
// one no matter how many malformed parts appear, since the set dedupes, which
// is what makes the resulting number quietly plausible rather than obviously
// broken.
//
// The row this pins that no other test supplies: a tool_call part with
// unrecognised Content. Every other Compute fixture builds parts through
// toolPart (a typed UnifiedToolCall, always named) or the JSON-round-tripped
// map shape; neither can produce an unnamed call.
//
// Mandatory mutation: drop `if call.Name != ""` (admit unconditionally) -> this
// must FAIL.
func TestComputeDoesNotCountUnnamedToolCallsAsDistinctTools(t *testing.T) {
	// Content is a bare string: not a UnifiedToolCall, not a map.
	malformed := transcript.UnifiedPart{Type: PartTypeToolCall, Content: "not a tool call"}

	entries := []transcript.UnifiedEntry{
		{Role: "assistant", Provider: "claude", Parts: []transcript.UnifiedPart{
			toolPart("Read", map[string]interface{}{"file_path": "/a"}),
			malformed,
			// A second malformed part: the distinct set dedupes, so a broken
			// guard inflates by one here rather than two — pinning the count at
			// 1 catches it either way.
			malformed,
		}},
	}

	// Premise: this really does yield an unnamed call, so the guard is the only
	// thing standing between it and the distinct set.
	if call := partToolCall(malformed); call.Name != "" {
		t.Fatalf("fixture premise broken: partToolCall returned name %q, want \"\"", call.Name)
	}

	got := Compute(entries)

	// All three are counted as CALLS — the guard must not suppress the call
	// itself, only its absent name.
	if iv(got.ToolCalls) != 3 {
		t.Errorf("ToolCalls = %d, want 3. A malformed tool_call part is still a tool "+
			"call that happened; only its NAME is unknown.", iv(got.ToolCalls))
	}
	if iv(got.DistinctTools) != 1 {
		t.Errorf("DistinctTools = %d, want 1 (only \"Read\"). An empty name is not a "+
			"tool: admitting it invents a distinct tool that was never invoked.",
			iv(got.DistinctTools))
	}
}

func TestComputeEmptyTranscript(t *testing.T) {
	got := Compute(nil)

	// The fold ran, so these are MEASURED zeros: non-nil pointers to 0. A nil
	// here would mean "not measured", which is a different claim (D4/D7).
	if got.ToolCalls == nil || got.DistinctTools == nil || got.Turns == nil {
		t.Fatalf("counts must be measured zeros, not nil: %+v", got.Process)
	}
	if iv(got.ToolCalls) != 0 || iv(got.DistinctTools) != 0 || iv(got.Turns) != 0 {
		t.Errorf("expected zeroed counts, got %+v", got.Process)
	}
	// No provider means nothing is measurable.
	if got.FilesTouched != nil {
		t.Errorf("FilesTouched = %d, want nil for an unknown provider", *got.FilesTouched)
	}
	// No usable timestamps: the wall clock is NOT MEASURED, which must be nil
	// rather than a 0 that reads as "finished instantly".
	if got.Diagnostics.WallClockSeconds != nil {
		t.Errorf("WallClockSeconds = %v, want nil (unmeasured)",
			*got.Diagnostics.WallClockSeconds)
	}
}

// --- Content dual-shape hazard -------------------------------------------

// UnifiedPart.Content degrades from typed structs to map[string]interface{}
// after any JSON round-trip. Both shapes must fold identically.
func TestComputeHandlesJSONRoundTrippedContent(t *testing.T) {
	entries := []transcript.UnifiedEntry{
		{Role: "user", Provider: "claude", Timestamp: time.Unix(100, 0),
			Parts: []transcript.UnifiedPart{textPart("do it")}},
		{Role: "assistant", Provider: "claude", Timestamp: time.Unix(160, 0),
			Tokens: &transcript.UnifiedTokens{Input: 10, Output: 5, CacheWrite: 2},
			Parts: []transcript.UnifiedPart{
				toolPart("Read", map[string]interface{}{"file_path": "/repo/a.go"}),
				toolPart("Write", map[string]interface{}{"file_path": "/repo/b.go"}),
			}},
	}

	typed := Compute(entries)

	// Round-trip the entries so Content becomes map[string]interface{}.
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var roundTripped []transcript.UnifiedEntry
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Guard the premise: if this ever stops being a map, the dual path is dead
	// code and this test is no longer testing what it claims.
	if _, ok := roundTripped[1].Parts[0].Content.(map[string]interface{}); !ok {
		t.Fatalf("premise broken: round-tripped Content is %T, expected map", roundTripped[1].Parts[0].Content)
	}

	mapped := Compute(roundTripped)

	if iv(typed.ToolCalls) != iv(mapped.ToolCalls) {
		t.Errorf("ToolCalls typed=%d mapped=%d", iv(typed.ToolCalls), iv(mapped.ToolCalls))
	}
	if iv(typed.DistinctTools) != iv(mapped.DistinctTools) {
		t.Errorf("DistinctTools typed=%d mapped=%d", iv(typed.DistinctTools), iv(mapped.DistinctTools))
	}
	if iv(typed.Turns) != iv(mapped.Turns) {
		t.Errorf("Turns typed=%d mapped=%d", iv(typed.Turns), iv(mapped.Turns))
	}
	if deref(t, typed.FilesTouched, "typed") != deref(t, mapped.FilesTouched, "mapped") {
		t.Errorf("FilesTouched typed=%v mapped=%v", typed.TouchedFiles, mapped.TouchedFiles)
	}
	if deref(t, typed.FilesEdited, "typed") != deref(t, mapped.FilesEdited, "mapped") {
		t.Errorf("FilesEdited typed=%v mapped=%v", typed.EditedFiles, mapped.EditedFiles)
	}
}

// --- Diagnostics quarantine ----------------------------------------------

func TestComputeDiagnostics(t *testing.T) {
	entries := []transcript.UnifiedEntry{
		{Role: "user", Provider: "claude", Timestamp: time.Unix(1000, 0),
			Parts: []transcript.UnifiedPart{textPart("hi")}},
		// A zero timestamp must not drag the window to the epoch.
		{Role: "assistant", Provider: "claude", Timestamp: time.Time{},
			Tokens: &transcript.UnifiedTokens{Input: 1, Output: 1}},
		{Role: "assistant", Provider: "claude", Timestamp: time.Unix(1090, 0),
			Tokens: &transcript.UnifiedTokens{
				Input: 100, Output: 20, Reasoning: 5, CacheRead: 7, CacheWrite: 3, Cost: 0.25,
			}},
	}

	got := Compute(entries)

	if fv(got.Diagnostics.WallClockSeconds) != 90 {
		t.Errorf("WallClockSeconds = %v, want 90", fv(got.Diagnostics.WallClockSeconds))
	}
	tok := got.Diagnostics.Tokens
	if tok.Input != 101 || tok.Output != 21 || tok.Reasoning != 5 || tok.CacheRead != 7 || tok.CacheWrite != 3 {
		t.Errorf("Tokens = %+v", tok)
	}
	if tok.Cost != 0.25 {
		t.Errorf("Cost = %v, want 0.25", tok.Cost)
	}
}

// Diagnostics must be nested, never inlined, so a joiner cannot accidentally
// map wall clock or tokens onto a scored axis such as record.Cost.
func TestDiagnosticsAreQuarantinedUnderSubObject(t *testing.T) {
	got := Compute([]transcript.UnifiedEntry{
		{Role: "assistant", Provider: "claude", Timestamp: time.Unix(1, 0),
			Tokens: &transcript.UnifiedTokens{Input: 5, Cost: 1.5}},
	})

	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Nothing token- or cost-shaped may appear at the top level.
	for _, key := range []string{"tokens", "cost", "wall_clock_seconds", "input", "output"} {
		if _, present := decoded[key]; present {
			t.Errorf("key %q at top level; diagnostics must stay nested", key)
		}
	}

	diag, ok := decoded["diagnostics"].(map[string]interface{})
	if !ok {
		t.Fatal("diagnostics sub-object missing")
	}
	if _, ok := diag["tokens"].(map[string]interface{}); !ok {
		t.Error("diagnostics.tokens missing")
	}
	if _, ok := diag["wall_clock_seconds"]; !ok {
		t.Error("diagnostics.wall_clock_seconds missing")
	}
}

// D4/D7: every pointer-shaped measurement must reach the wire as absent-or-null
// when unmeasured, never as a 0 a consumer would average in as a real
// observation. This is the serialisation half of the nil-means-not-measured
// contract, asserted on a Result that never went through the fold.
func TestUnmeasuredFieldsNeverSerialiseAsZero(t *testing.T) {
	data, err := json.Marshal(Result{Provider: "claude", SessionID: "s1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// All five embedded record.Process counts carry omitempty: absent.
	for _, key := range []string{
		"tool_calls", "distinct_tools", "files_touched", "forbidden_touches", "turns",
	} {
		if v, present := decoded[key]; present {
			t.Errorf("unmeasured %q reached the wire as %v; it must be omitted", key, v)
		}
	}

	// The diagnostic wall clock carries omitempty, so unmeasured is ABSENT.
	// If it is present at all it must be null; a number — 0 above all — is the
	// D4 lie this assertion exists to catch.
	diag, ok := decoded["diagnostics"].(map[string]interface{})
	if !ok {
		t.Fatalf("diagnostics sub-object missing: %s", data)
	}
	if v, present := diag["wall_clock_seconds"]; present && v != nil {
		t.Fatalf("unmeasured wall_clock_seconds = %v, want absent: %s", v, data)
	}
	if strings.Contains(string(data), `"wall_clock_seconds":0`) {
		t.Fatalf("unmeasured wall clock serialised as a measured zero: %s", data)
	}
}

// A measured zero must be preserved, which is the point of the pointers: a
// session that genuinely made no tool calls is a real observation.
func TestMeasuredZerosSurviveSerialisation(t *testing.T) {
	zero, zeroF := 0, 0.0
	r := Result{
		Provider: "claude",
		Process: record.Process{
			ToolCalls: &zero, DistinctTools: &zero, Turns: &zero,
			FilesTouched: &zero, ForbiddenTouches: &zero,
		},
		Diagnostics: Diagnostics{WallClockSeconds: &zeroF},
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var back Result
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for name, p := range map[string]*int{
		"tool_calls": back.ToolCalls, "distinct_tools": back.DistinctTools,
		"turns": back.Turns, "files_touched": back.FilesTouched,
		"forbidden_touches": back.ForbiddenTouches,
	} {
		if p == nil || *p != 0 {
			t.Errorf("measured zero %s collapsed to nil: %s", name, data)
		}
	}
	if back.Diagnostics.WallClockSeconds == nil || *back.Diagnostics.WallClockSeconds != 0 {
		t.Errorf("measured zero wall clock collapsed to nil: %s", data)
	}
}
