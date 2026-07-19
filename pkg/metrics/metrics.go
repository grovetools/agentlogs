// Package metrics computes deterministic process metrics from a normalized
// agent transcript.
//
// The fold is PURE: Compute takes already-loaded entries and returns a Result
// with no I/O, no clock, no ambient state. Loading a transcript is the caller's
// job (see cmd/metrics.go) so that the same entries always produce the same
// numbers regardless of whether a daemon happens to be running.
//
// This package deliberately does not import internal/ or pkg/display.
package metrics

import (
	"strings"
	"time"

	"github.com/grovetools/eval/pkg/record"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

// Part type discriminators. transcript.UnifiedPart.Type carries bare string
// literals with no const block upstream (pkg/transcript/unified.go:23), so we
// define our own rather than scattering literals through the fold.
const (
	PartTypeText       = "text"
	PartTypeToolCall   = "tool_call"
	PartTypeToolResult = "tool_result"
	PartTypeReasoning  = "reasoning"
)

// Unsupported measurement identifiers, emitted in Result.Unsupported.
const (
	UnsupportedFilesTouched = "files_touched"
	UnsupportedFilesEdited  = "files_edited"
)

// Tokens mirrors transcript.UnifiedTokens as a summed diagnostic.
type Tokens struct {
	Input      int     `json:"input"`
	Output     int     `json:"output"`
	Reasoning  int     `json:"reasoning"`
	CacheRead  int     `json:"cache_read"`
	CacheWrite int     `json:"cache_write"`
	Cost       float64 `json:"cost"`
}

// Diagnostics holds CROSS-CHECK-ONLY numbers.
//
// Nothing under this object is an evaluation axis and nothing under it may be
// mapped into record.Cost or any other scored field by a joiner. Token counts
// and wall-clock time are provider-reported, non-deterministic across replays,
// and (for wall clock) sensitive to queueing and human think-time. They live
// here quarantined so that reading them requires deliberately reaching into a
// sub-object named "diagnostics".
type Diagnostics struct {
	Tokens Tokens `json:"tokens"`
	// WallClockSeconds is the span between the first and last non-zero entry
	// timestamp. It includes any time the agent spent blocked or idle.
	// Pointer for the same D4 reason as record.Cost.WallClockSeconds: a
	// transcript with no usable timestamps is "not measured", which a plain
	// float64 would silently report as 0. omitempty matches
	// record.Cost.WallClockSeconds: unmeasured is absent. omitempty on a
	// pointer omits only a nil pointer and never inspects the pointee, so a
	// measured 0 still serialises.
	WallClockSeconds *float64 `json:"wall_clock_seconds,omitempty"`
}

// Result is the full metrics fold for one session.
//
// record.Process is embedded, so its fields (tool_calls, distinct_tools,
// files_touched, forbidden_touches, turns) marshal inline at the top level.
// FilesTouched and ForbiddenTouches are pointers: nil means "not measured",
// never zero.
type Result struct {
	Provider  string `json:"provider"`
	SessionID string `json:"session_id"`

	record.Process

	// FilesEdited counts distinct files mutated (a subset of FilesTouched).
	// record.Process has no such field, so it rides alongside. nil means "not
	// measured", matching the embedded pointer semantics.
	FilesEdited *int `json:"files_edited,omitempty"`

	// TouchedFiles / EditedFiles are the underlying path lists, emitted only
	// when the caller asks (aglogs metrics --files). agentlogs is
	// fixture-ignorant: it does not know which paths a fixture forbids, so it
	// publishes the list and lets eval compute ForbiddenTouches at join time.
	TouchedFiles []string `json:"touched_files,omitempty"`
	EditedFiles  []string `json:"edited_files,omitempty"`

	// Unsupported lists measurements this provider cannot produce. Present
	// only when non-empty. A consumer seeing a nil count should look here to
	// distinguish "measured zero" from "cannot measure".
	Unsupported []string `json:"unsupported,omitempty"`

	Diagnostics Diagnostics `json:"diagnostics"`
}

// Compute folds normalized transcript entries into process metrics.
//
// Sidechain (subagent) entries are excluded from every count: they represent
// work delegated inside a run, and counting them would double-count the parent
// agent's tool budget.
//
// SessionID is not carried on UnifiedEntry and is left empty for the caller to
// populate.
func Compute(entries []transcript.UnifiedEntry) Result {
	var result Result

	toolCalls := 0
	distinctTools := make(map[string]struct{})
	turns := 0
	touches := newFileTouches()

	var tokens Tokens
	var firstTS, lastTS time.Time

	// Provider is taken from the first entry that declares one; a transcript is
	// single-provider by construction.
	provider := ""
	for _, entry := range entries {
		if entry.IsSidechain {
			continue
		}
		if provider == "" && entry.Provider != "" {
			provider = entry.Provider
		}
	}
	result.Provider = provider

	for _, entry := range entries {
		// Sidechain entries are excluded from every count.
		if entry.IsSidechain {
			continue
		}

		if !entry.Timestamp.IsZero() {
			if firstTS.IsZero() || entry.Timestamp.Before(firstTS) {
				firstTS = entry.Timestamp
			}
			if lastTS.IsZero() || entry.Timestamp.After(lastTS) {
				lastTS = entry.Timestamp
			}
		}

		if entry.Tokens != nil {
			tokens.Input += entry.Tokens.Input
			tokens.Output += entry.Tokens.Output
			tokens.Reasoning += entry.Tokens.Reasoning
			tokens.CacheRead += entry.Tokens.CacheRead
			tokens.CacheWrite += entry.Tokens.CacheWrite
			tokens.Cost += entry.Tokens.Cost
		}

		hasText := false
		for _, part := range entry.Parts {
			switch part.Type {
			case PartTypeToolCall:
				call := partToolCall(part)
				toolCalls++
				if call.Name != "" {
					// Case-preserved: "Read" and "read" are distinct names.
					distinctTools[call.Name] = struct{}{}
				}
				touches.observe(provider, call)
			case PartTypeText:
				if strings.TrimSpace(partText(part)) != "" {
					hasText = true
				}
			}
		}

		// A turn is a user message that actually says something. User entries
		// that only carry tool_result parts are transport, not turns.
		if entry.Role == "user" && hasText {
			turns++
		}
	}

	// These three are genuinely measured whenever the fold runs — a session
	// with no tool calls really did make zero of them — so they are always
	// assigned. The pointers exist so a consumer that never ran the fold is
	// distinguishable from one that measured zero (D4/D7).
	distinctCount := len(distinctTools)
	result.Process = record.Process{
		ToolCalls:     &toolCalls,
		DistinctTools: &distinctCount,
		Turns:         &turns,
	}

	if providerSupported(provider) {
		touched := touches.touchedList()
		edited := touches.editedList()
		touchedCount := len(touched)
		editedCount := len(edited)
		result.FilesTouched = &touchedCount
		result.FilesEdited = &editedCount
		result.TouchedFiles = touched
		result.EditedFiles = edited
	} else {
		// Leave FilesTouched/FilesEdited nil — "not measured", not zero.
		result.Unsupported = []string{UnsupportedFilesTouched, UnsupportedFilesEdited}
	}

	// ForbiddenTouches is never computed here. It requires the fixture manifest
	// of forbidden paths, which is eval's input, not agentlogs'. It stays nil
	// and eval fills it at join time from TouchedFiles.

	result.Diagnostics = Diagnostics{Tokens: tokens}
	if !firstTS.IsZero() && !lastTS.IsZero() {
		wc := lastTS.Sub(firstTS).Seconds()
		result.Diagnostics.WallClockSeconds = &wc
	}

	return result
}

// --- Content dual-shape accessors ---------------------------------------
//
// UnifiedPart.Content is interface{}. It holds a typed struct when the
// normalizer ran in-process, but degrades to map[string]interface{} after any
// JSON round-trip. Every reader must therefore try the typed assertion first
// and fall back to the map form. This mirrors the (unexported) pattern in
// pkg/display/render.go:349-402; it is duplicated rather than imported because
// pkg/metrics must not depend on pkg/display.

// partToolCall extracts a UnifiedToolCall from a "tool_call" part.
func partToolCall(part transcript.UnifiedPart) transcript.UnifiedToolCall {
	if content, ok := part.Content.(transcript.UnifiedToolCall); ok {
		return content
	}
	if contentMap, ok := part.Content.(map[string]interface{}); ok {
		call := transcript.UnifiedToolCall{
			ID:     getStringField(contentMap, "id"),
			Name:   getStringField(contentMap, "name"),
			Status: getStringField(contentMap, "status"),
			Output: getStringField(contentMap, "output"),
			Title:  getStringField(contentMap, "title"),
			Diff:   getStringField(contentMap, "diff"),
		}
		// Only set Input if it really is an object; a non-object leaves it nil
		// and the file-touch table simply finds nothing.
		if input, ok := contentMap["input"].(map[string]interface{}); ok {
			call.Input = input
		}
		return call
	}
	return transcript.UnifiedToolCall{}
}

// partText extracts text from a "text" part.
func partText(part transcript.UnifiedPart) string {
	if content, ok := part.Content.(transcript.UnifiedTextContent); ok {
		return content.Text
	}
	if contentMap, ok := part.Content.(map[string]interface{}); ok {
		return getStringField(contentMap, "text")
	}
	return ""
}

// getStringField safely extracts a string field from a map.
func getStringField(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
