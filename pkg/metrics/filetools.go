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
	{Provider: "claude", Tool: "edit", InputKeys: []string{"file_path"}, Edit: true},
	{Provider: "claude", Tool: "write", InputKeys: []string{"file_path"}, Edit: true},
	{Provider: "claude", Tool: "multiedit", InputKeys: []string{"file_path"}, Edit: true},
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
	{Provider: "pi", Tool: "read", InputKeys: []string{"path"}, Edit: false},
	{Provider: "pi", Tool: "grep", InputKeys: []string{"path"}, Edit: false},
	{Provider: "pi", Tool: "find", InputKeys: []string{"path"}, Edit: false},
	{Provider: "pi", Tool: "ls", InputKeys: []string{"path"}, Edit: false},
	{Provider: "pi", Tool: "edit", InputKeys: []string{"path"}, Edit: true},
	{Provider: "pi", Tool: "write", InputKeys: []string{"path"}, Edit: true},
}

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
	p := strings.ToLower(provider)
	name := strings.ToLower(call.Name)

	for _, rule := range fileToolTable {
		if rule.Provider != p || rule.Tool != name {
			continue
		}
		path := firstStringValue(call.Input, rule.InputKeys)
		if path == "" {
			continue
		}
		// An edit is also a touch.
		f.touched[path] = struct{}{}
		if rule.Edit {
			f.edited[path] = struct{}{}
		}
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
