package transcript

import (
	"strings"
	"testing"
)

func TestJournalNormalizerStarted(t *testing.T) {
	n := NewJournalNormalizer()
	line := `{"type":"started","key":"v2:5c6d5b0d80077c4519bbe2738eba4eb252db95fa5ed6fa8cf132eb2ccc300a17","agentId":"a6053d9fe85440cfe"}`

	entry, err := n.NormalizeLine([]byte(line))
	if err != nil {
		t.Fatalf("NormalizeLine: %v", err)
	}
	if entry == nil {
		t.Fatal("expected entry, got nil")
	}
	if entry.AgentID != "a6053d9fe85440cfe" {
		t.Errorf("AgentID = %q, want a6053d9fe85440cfe", entry.AgentID)
	}
	if !entry.IsSidechain {
		t.Error("IsSidechain = false, want true")
	}
	if entry.Provider != "journal" {
		t.Errorf("Provider = %q, want journal", entry.Provider)
	}
}

func TestJournalNormalizerResult(t *testing.T) {
	n := NewJournalNormalizer()
	line := `{"type":"result","key":"v2:abc","agentId":"a123","result":{"summary":"done","count":3}}`

	entry, err := n.NormalizeLine([]byte(line))
	if err != nil {
		t.Fatalf("NormalizeLine: %v", err)
	}
	if entry == nil {
		t.Fatal("expected entry, got nil")
	}
	if entry.AgentID != "a123" {
		t.Errorf("AgentID = %q, want a123", entry.AgentID)
	}
	if len(entry.Parts) == 0 {
		t.Fatal("expected at least one part")
	}
	text, ok := entry.Parts[0].Content.(UnifiedTextContent)
	if !ok {
		t.Fatalf("part content type %T, want UnifiedTextContent", entry.Parts[0].Content)
	}
	if !strings.Contains(text.Text, `"summary"`) {
		t.Errorf("result payload not rendered in text: %q", text.Text)
	}
}

func TestJournalNormalizerUnknownTypeSkipped(t *testing.T) {
	n := NewJournalNormalizer()
	// Format-drift guard: unknown event types must be skipped, not errors.
	entry, err := n.NormalizeLine([]byte(`{"type":"phase_changed","key":"v2:x","agentId":"a1"}`))
	if err != nil {
		t.Fatalf("unknown type should not error, got %v", err)
	}
	if entry != nil {
		t.Errorf("unknown type should be skipped, got %+v", entry)
	}
}

func TestJournalNormalizerMalformedLine(t *testing.T) {
	n := NewJournalNormalizer()
	if _, err := n.NormalizeLine([]byte(`{not json`)); err == nil {
		t.Error("malformed line should return an error")
	}
}
