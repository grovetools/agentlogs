package transcript

import (
	"testing"
	"time"

	"github.com/grovetools/agentlogs/internal/opencode"
)

func TestOpenCodeNormalizeEntry(t *testing.T) {
	n := NewOpenCodeNormalizer()

	entry := opencode.TranscriptEntry{
		Role:      "assistant",
		Timestamp: time.Unix(1751400010, 0),
		MessageID: "msg_0002",
		Tokens: &opencode.TokenUsage{
			Input:      120,
			Output:     45,
			Reasoning:  10,
			CacheRead:  300,
			CacheWrite: 80,
		},
		Parts: []opencode.Part{
			{ID: "prt_0001", Type: "text", Content: opencode.TextPart{Text: "I fixed the bug."}},
			{ID: "prt_0002", Type: "tool", Content: opencode.ToolPart{
				CallID: "call_001",
				Tool:   "edit",
				Status: "completed",
				Input:  map[string]interface{}{"filePath": "main.go"},
				Output: "edited",
				Title:  "Edit main.go",
				Diff:   "-old\n+new",
			}},
			{ID: "prt_0003", Type: "patch", Content: opencode.PatchPart{
				Hash:  "abc123def4567890",
				Files: []string{"main.go", "main_test.go"},
			}},
			{ID: "prt_0004", Type: "step-finish", Content: map[string]interface{}{"reason": "done"}},
		},
	}

	unified := n.NormalizeEntry(entry)
	if unified == nil {
		t.Fatal("NormalizeEntry returned nil")
	}
	if unified.Provider != "opencode" || unified.Role != "assistant" || unified.MessageID != "msg_0002" {
		t.Errorf("header = %+v", unified)
	}
	if unified.Tokens == nil || unified.Tokens.Input != 120 || unified.Tokens.CacheWrite != 80 {
		t.Errorf("tokens = %+v", unified.Tokens)
	}

	// step-finish is skipped; text + tool + patch survive.
	if len(unified.Parts) != 3 {
		t.Fatalf("got %d parts, want 3", len(unified.Parts))
	}

	if unified.Parts[0].Type != "text" {
		t.Errorf("part 0 type = %q, want text", unified.Parts[0].Type)
	}
	if unified.Parts[1].Type != "tool_call" {
		t.Errorf("part 1 type = %q, want tool_call", unified.Parts[1].Type)
	}

	// The patch part must not be dropped: it renders as a tool_call-like
	// part carrying the snapshot hash + file list.
	patchPart := unified.Parts[2]
	if patchPart.Type != "tool_call" {
		t.Fatalf("patch part type = %q, want tool_call", patchPart.Type)
	}
	tc, ok := patchPart.Content.(UnifiedToolCall)
	if !ok {
		t.Fatalf("patch part content = %#v, want UnifiedToolCall", patchPart.Content)
	}
	if tc.Name != "patch" || tc.ID != "prt_0003" || tc.Status != "completed" {
		t.Errorf("patch tool call = %+v", tc)
	}
	if tc.Input["hash"] != "abc123def4567890" {
		t.Errorf("patch hash = %v", tc.Input["hash"])
	}
	files, ok := tc.Input["files"].([]string)
	if !ok || len(files) != 2 {
		t.Errorf("patch files = %#v", tc.Input["files"])
	}
	if tc.Title != "patch abc123de (2 files)" {
		t.Errorf("patch title = %q", tc.Title)
	}
}

func TestOpenCodeNormalizeEntryPatchOnly(t *testing.T) {
	n := NewOpenCodeNormalizer()
	entry := opencode.TranscriptEntry{
		Role:      "assistant",
		MessageID: "msg_p",
		Parts: []opencode.Part{
			{ID: "prt_p1", Type: "patch", Content: opencode.PatchPart{Files: []string{"a.go"}}},
		},
	}
	unified := n.NormalizeEntry(entry)
	if len(unified.Parts) != 1 {
		t.Fatalf("got %d parts, want 1", len(unified.Parts))
	}
	tc := unified.Parts[0].Content.(UnifiedToolCall)
	if tc.Title != "patch (1 file)" {
		t.Errorf("title = %q", tc.Title)
	}
}

// TestOpenCodeNormalizeFromFixtures exercises the assembler -> normalizer
// pipeline end to end against the storage fixtures.
func TestOpenCodeNormalizeFromFixtures(t *testing.T) {
	a, err := opencode.NewAssemblerWithDir("../../internal/opencode/testdata/storage")
	if err != nil {
		t.Fatalf("NewAssemblerWithDir: %v", err)
	}
	entries, err := a.AssembleTranscript("ses_fixture01")
	if err != nil {
		t.Fatalf("AssembleTranscript: %v", err)
	}

	unified := NewOpenCodeNormalizer().NormalizeAll(entries)
	if len(unified) != 2 {
		t.Fatalf("got %d unified entries, want 2", len(unified))
	}

	var sawPatch bool
	for _, p := range unified[1].Parts {
		if tc, ok := p.Content.(UnifiedToolCall); ok && tc.Name == "patch" {
			sawPatch = true
		}
	}
	if !sawPatch {
		t.Error("assistant entry lost its patch part through the pipeline")
	}
}
