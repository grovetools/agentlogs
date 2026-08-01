package metrics

import (
	"sort"
	"strings"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

// fileToolRule describes how to extract file paths from one provider's tool
// call. Tool is matched case-insensitively (lowercased on both sides) because
// providers are inconsistent about casing. InputKeys are tried in order and the
// FIRST key that yields a non-empty string wins — they are alternate spellings
// of the same argument, not a list of separate files.
//
// Edit distinguishes a mutation (Write/Edit/...) from a mere observation
// (Read/Grep). Edited files are a subset of touched files.
type fileToolRule struct {
	Provider  string
	Tool      string
	InputKeys []string
	Edit      bool

	// OldKeys / NewKeys name the input keys carrying the text a mutating call
	// REPLACED and the text it wrote in its place; when both resolve, the call's
	// line churn is measurable (see lineChurn). EditsKey instead names an
	// array-valued key whose elements are themselves {old,new} objects, each
	// read with the same OldKeys/NewKeys spellings and summed.
	//
	// A rule may set neither, which means "this call mutates the file but its
	// churn is not derivable from the transcript" — Write carries only the new
	// contents, so there is no way to tell a 400-line new file from a 400-line
	// rewrite of a 400-line file. Such a call reports zero churn and is
	// deliberately NOT counted, in keeping with this table's undercount-rather-
	// than-guess rule: a consumer renders "an agent wrote this" without a line
	// count rather than a number that would contradict git's own.
	OldKeys  []string
	NewKeys  []string
	EditsKey string
}

// fileToolTable is the entire file-touch vocabulary this package understands.
//
// It is deliberately, aggressively incomplete. A tool absent from this table
// contributes NOTHING to the counts — we undercount rather than guess. Adding a
// provider means adding rows here and nothing else; a provider with zero rows
// is reported as unsupported (nil counts), never as zero.
//
// Claude rows are authored from the Claude Code tool schema. The input-key
// spellings "file_path", "old_string", "new_string" and "content" are confirmed
// in-repo at pkg/formatters/formatters.go:60-63 and :132. The "path" (Grep) and
// "notebook_path" (NotebookEdit) spellings are NOT attested anywhere in
// agentlogs — they come from the upstream tool schema. Both rules degrade to
// contributing nothing if the spelling is wrong, so a mistake here undercounts
// rather than corrupts.
var fileToolTable = []fileToolRule{
	{Provider: "claude", Tool: "read", InputKeys: []string{"file_path"}, Edit: false},
	{Provider: "claude", Tool: "grep", InputKeys: []string{"path"}, Edit: false},
	{
		Provider: "claude", Tool: "edit", InputKeys: []string{"file_path"}, Edit: true,
		OldKeys: []string{"old_string"}, NewKeys: []string{"new_string"},
	},
	{Provider: "claude", Tool: "write", InputKeys: []string{"file_path"}, Edit: true},
	{
		Provider: "claude", Tool: "multiedit", InputKeys: []string{"file_path"}, Edit: true,
		OldKeys: []string{"old_string"}, NewKeys: []string{"new_string"}, EditsKey: "edits",
	},
	{Provider: "claude", Tool: "notebookedit", InputKeys: []string{"notebook_path", "file_path"}, Edit: true},

	// pi rows. pi's entire tool vocabulary is read/bash/edit/write/grep/find/ls
	// (ToolName + allToolNames, packages/coding-agent/src/core/tools/index.ts),
	// and every file-taking tool spells the argument "path" — verified in each
	// tool's typebox schema: read.ts readSchema.path (required), write.ts
	// writeSchema.path (required), edit.ts editSchema.path (required, alongside
	// an `edits` array), grep.ts/find.ts/ls.ts *Schema.path (optional).
	//
	// bash is deliberately absent: bashSchema is {command, timeout} only, so
	// there is genuinely no structured path to read. That is the one tool the
	// original "pi is unsupported" note was actually describing.
	//
	// No pi row carries churn keys. pi's edit takes an `edits` array, but the
	// spelling of the keys INSIDE its elements is not attested anywhere in this
	// repo, and the churn fields exist to be exact — a guessed spelling would
	// silently report zero anyway (see fileToolRule.OldKeys). pi edits therefore
	// register as mutations with unmeasured churn.
	{Provider: "pi", Tool: "read", InputKeys: []string{"path"}, Edit: false},
	{Provider: "pi", Tool: "grep", InputKeys: []string{"path"}, Edit: false},
	{Provider: "pi", Tool: "find", InputKeys: []string{"path"}, Edit: false},
	{Provider: "pi", Tool: "ls", InputKeys: []string{"path"}, Edit: false},
	{Provider: "pi", Tool: "edit", InputKeys: []string{"path"}, Edit: true},
	{Provider: "pi", Tool: "write", InputKeys: []string{"path"}, Edit: true},
}

// FileTouch is one file-taking tool call resolved against the file-tool table:
// the path the call named, whether the call mutated it, and how many lines that
// mutation added and removed.
//
// LinesAdded/LinesRemoved are the call's OWN churn, measured from the tool
// arguments (see lineChurn) — not the file's total churn against git, which the
// transcript cannot know. They are zero for a read, and also zero for a
// mutation whose churn is not derivable (Write, pi edit; see fileToolRule), so
// a consumer must treat 0/0 on an Edit as "unmeasured", never as "no change".
type FileTouch struct {
	Path         string
	Edit         bool
	LinesAdded   int
	LinesRemoved int
}

// ToolCallFileTouch resolves a single tool call to the file it touched, or
// reports false when the (provider, tool) pair is not in the table or the call
// carries no usable path. It is the per-call primitive behind this package's
// session roll-ups, exported so live consumers — treemux's accessed-files
// drawer streams a transcript entry at a time — classify tool calls with the
// SAME vocabulary rather than a parallel, quietly diverging table of their own.
func ToolCallFileTouch(provider string, call transcript.UnifiedToolCall) (FileTouch, bool) {
	p := strings.ToLower(provider)
	name := strings.ToLower(call.Name)
	for _, rule := range fileToolTable {
		if rule.Provider != p || rule.Tool != name {
			continue
		}
		if path := firstStringValue(call.Input, rule.InputKeys); path != "" {
			added, removed := ruleChurn(rule, call.Input)
			return FileTouch{Path: path, Edit: rule.Edit, LinesAdded: added, LinesRemoved: removed}, true
		}
	}
	return FileTouch{}, false
}

// ruleChurn measures one call's line churn through rule's churn keys: a single
// old/new pair, or every element of an EditsKey array summed (one MultiEdit call
// is several replacements in one file). A rule with no churn keys, or arguments
// that do not carry them, yields 0/0 — unmeasured, not "unchanged".
func ruleChurn(rule fileToolRule, input map[string]interface{}) (added, removed int) {
	if input == nil || (len(rule.OldKeys) == 0 && len(rule.NewKeys) == 0) {
		return 0, 0
	}
	if rule.EditsKey != "" {
		edits, ok := input[rule.EditsKey].([]interface{})
		if ok {
			for _, e := range edits {
				elem, ok := e.(map[string]interface{})
				if !ok {
					continue
				}
				a, r := lineChurn(firstStringValue(elem, rule.OldKeys), firstStringValue(elem, rule.NewKeys))
				added += a
				removed += r
			}
			return added, removed
		}
		// An EditsKey rule whose call carries the pair directly is still
		// measurable — the multi-edit spelling is a superset of the single one,
		// so fall through rather than reporting nothing.
	}
	return lineChurn(firstStringValue(input, rule.OldKeys), firstStringValue(input, rule.NewKeys))
}

// lineChurn measures how many lines a replacement added and removed, after
// discarding the common leading and trailing lines the edit left untouched.
//
// This is a diff-free approximation of `git diff --numstat` for one hunk: an
// agent's old_string/new_string usually share several anchor lines (that is how
// the edit is made unique), and counting those as both added and removed would
// roughly double every number. Trimming them costs nothing and lands on git's
// figure whenever the changed lines are contiguous, which they are for the great
// majority of edits. It does NOT run an LCS, so an edit that changes the first
// and last line of a large region still reports the whole region — an
// overstatement bounded by the size of the string the agent actually passed.
func lineChurn(oldText, newText string) (added, removed int) {
	if oldText == newText {
		return 0, 0
	}
	oldLines, newLines := splitLines(oldText), splitLines(newText)
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix &&
		oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}
	return len(newLines) - prefix - suffix, len(oldLines) - prefix - suffix
}

// splitLines counts a tool argument's lines the way a diff would: an empty
// string is no lines at all (an insertion removes nothing), and a single
// trailing newline terminates the last line rather than starting an empty one.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// ProviderTracksFileTouches is the exported form of providerSupported: it
// reports whether file access can be measured for a provider at all, so a
// caller can say "not measurable here" rather than render a misleading empty
// list.
func ProviderTracksFileTouches(provider string) bool { return providerSupported(provider) }

// providerSupported reports whether the file-touch table knows anything at all
// about a provider. Providers it does not know yield nil file counts plus an
// "unsupported" list, never a misleading zero.
//
// claude and pi are supported. The rest are unsupported, each for a concrete
// evidentiary reason rather than by omission:
//
//   - codex: Input["command"] is an argv ARRAY (["bash","-lc","ls *.go"]), not
//     a path — see pkg/display/unified.go:152-158 and the codex fixture. There
//     is no structured file-path key to read, and apply_patch does not appear
//     anywhere in agentlogs.
//   - opencode: the file-path key IS known ("filePath",
//     pkg/display/opencode.go:109), but opencode tool NAMES are nowhere in the
//     repo — the normalizer passes toolPart.Tool through opaquely
//     (pkg/transcript/normalizer_opencode.go:65). Without names we cannot tell
//     a read from a write, so we decline to measure rather than guess.
//
// pi was previously listed here as unsupported on the grounds that "the only
// tool in the pi fixture is bash". That was a statement about the FIXTURE being
// read as a statement about the provider: pi's real vocabulary has six
// file-taking tools, all keyed "path". Reporting "cannot measure" for something
// measurable is a D4 error in the optimistic direction, so the rows were added
// and the claim retired. Do not re-derive it from a thin fixture.
func providerSupported(provider string) bool {
	p := strings.ToLower(provider)
	for _, rule := range fileToolTable {
		if rule.Provider == p {
			return true
		}
	}
	return false
}

// fileTouches accumulates the distinct touched/edited path sets for a session.
type fileTouches struct {
	touched map[string]struct{}
	edited  map[string]struct{}
}

func newFileTouches() *fileTouches {
	return &fileTouches{
		touched: make(map[string]struct{}),
		edited:  make(map[string]struct{}),
	}
}

// observe applies the table to a single tool call. Unknown provider/tool pairs
// and rules whose keys are absent or non-string are silently ignored.
func (f *fileTouches) observe(provider string, call transcript.UnifiedToolCall) {
	touch, ok := ToolCallFileTouch(provider, call)
	if !ok {
		return
	}
	// An edit is also a touch.
	f.touched[touch.Path] = struct{}{}
	if touch.Edit {
		f.edited[touch.Path] = struct{}{}
	}
}

// firstStringValue returns the first key in keys whose value is a non-empty
// string. Alternate spellings, not separate files.
func firstStringValue(input map[string]interface{}, keys []string) string {
	if input == nil {
		return ""
	}
	for _, key := range keys {
		if v, ok := input[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// touchedList returns the distinct touched paths, sorted for determinism.
func (f *fileTouches) touchedList() []string { return sortedKeys(f.touched) }

// editedList returns the distinct edited paths, sorted for determinism.
func (f *fileTouches) editedList() []string { return sortedKeys(f.edited) }

func sortedKeys(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
