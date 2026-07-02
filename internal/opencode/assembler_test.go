package opencode

import (
	"testing"
)

func fixtureAssembler(t *testing.T) *Assembler {
	t.Helper()
	a, err := NewAssemblerWithDir("testdata/storage")
	if err != nil {
		t.Fatalf("NewAssemblerWithDir: %v", err)
	}
	return a
}

func TestAssembleTranscript(t *testing.T) {
	a := fixtureAssembler(t)

	entries, err := a.AssembleTranscript("ses_fixture01")
	if err != nil {
		t.Fatalf("AssembleTranscript: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	user := entries[0]
	if user.Role != "user" || user.MessageID != "msg_0001" {
		t.Errorf("entry 0 = role %q id %q, want user/msg_0001", user.Role, user.MessageID)
	}
	if len(user.Parts) != 1 {
		t.Fatalf("user entry has %d parts, want 1", len(user.Parts))
	}
	if text, ok := user.Parts[0].Content.(TextPart); !ok || text.Text != "Please fix the bug in main.go" {
		t.Errorf("user text part = %#v", user.Parts[0].Content)
	}
	if user.Tokens != nil {
		t.Errorf("user entry should have no tokens, got %+v", user.Tokens)
	}

	asst := entries[1]
	if asst.Role != "assistant" || asst.MessageID != "msg_0002" {
		t.Errorf("entry 1 = role %q id %q, want assistant/msg_0002", asst.Role, asst.MessageID)
	}
	if asst.Tokens == nil {
		t.Fatal("assistant entry missing tokens")
	}
	if asst.Tokens.Input != 120 || asst.Tokens.Output != 45 || asst.Tokens.Reasoning != 10 ||
		asst.Tokens.CacheRead != 300 || asst.Tokens.CacheWrite != 80 {
		t.Errorf("assistant tokens = %+v", asst.Tokens)
	}
	if len(asst.Parts) != 3 {
		t.Fatalf("assistant entry has %d parts, want 3 (text, tool, patch)", len(asst.Parts))
	}

	tool, ok := asst.Parts[1].Content.(ToolPart)
	if !ok {
		t.Fatalf("part 1 content = %#v, want ToolPart", asst.Parts[1].Content)
	}
	if tool.Tool != "edit" || tool.Status != "completed" || tool.Diff != "-old\n+new" {
		t.Errorf("tool part = %+v", tool)
	}

	patch, ok := asst.Parts[2].Content.(PatchPart)
	if !ok {
		t.Fatalf("part 2 content = %#v, want PatchPart", asst.Parts[2].Content)
	}
	if patch.Hash != "abc123def4567890" {
		t.Errorf("patch hash = %q", patch.Hash)
	}
	if len(patch.Files) != 2 || patch.Files[0] != "main.go" || patch.Files[1] != "main_test.go" {
		t.Errorf("patch files = %v", patch.Files)
	}
}

func TestAssembleTranscriptUnknownSession(t *testing.T) {
	a := fixtureAssembler(t)
	if _, err := a.AssembleTranscript("ses_missing"); err == nil {
		t.Fatal("expected error for unknown session")
	}
}

func TestNewAssemblerWithDirMissing(t *testing.T) {
	if _, err := NewAssemblerWithDir("testdata/does-not-exist"); err == nil {
		t.Fatal("expected error for missing storage dir")
	}
}
