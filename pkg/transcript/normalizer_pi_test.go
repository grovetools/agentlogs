package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// piFixturePath is a real-shaped pi v3 session transcript in pi's munged-cwd
// layout (~/.pi/agent/sessions/--<cwd-with-dashes>--/<ts>_<uuid>.jsonl, see
// session-manager.ts in the pi source). It contains one abandoned branch:
// aa000004 is a child of aa000003 that was branched away from (bb000004 is
// the retry with the same parent), so the active path must skip it.
const piFixturePath = "testdata/pi/sessions/--Users-test-project--/2026-07-01T10-00-00-000Z_0198c2f4-9a51-7abc-8def-0123456789ab.jsonl"

func TestPiSessionDirName(t *testing.T) {
	cases := map[string]string{
		"/Users/test/project": "--Users-test-project--",
		"/tmp/a b/c":          "--tmp-a b-c--", // spaces survive; only / \ : are munged
		`C:\work\repo`:        "--C--work-repo--",
		"/Users/x:y/z":        "--Users-x-y-z--",
	}
	for in, want := range cases {
		if got := PiSessionDirName(in); got != want {
			t.Errorf("PiSessionDirName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPiSessionsGlob(t *testing.T) {
	got := PiSessionsGlob("/home/u", "")
	want := filepath.Join("/home/u", ".pi", "agent", "sessions", "*", "*.jsonl")
	if got != want {
		t.Errorf("PiSessionsGlob = %q, want %q", got, want)
	}
	gotID := PiSessionsGlob("/home/u", "abc-123")
	wantID := filepath.Join("/home/u", ".pi", "agent", "sessions", "*", "*abc-123*.jsonl")
	if gotID != wantID {
		t.Errorf("PiSessionsGlob(id) = %q, want %q", gotID, wantID)
	}
}

func TestPiNormalizer_NormalizeLine_UserStringContent(t *testing.T) {
	n := NewPiNormalizer()
	line := `{"type":"message","id":"e1","parentId":null,"timestamp":"2026-07-01T10:00:01.000Z","message":{"role":"user","content":"hello there","timestamp":1782900001000}}`

	entry, err := n.NormalizeLine([]byte(line))
	if err != nil {
		t.Fatalf("NormalizeLine: %v", err)
	}
	if entry == nil || entry.Role != "user" || entry.Provider != "pi" {
		t.Fatalf("unexpected entry: %+v", entry)
	}
	if entry.MessageID != "e1" {
		t.Errorf("MessageID = %q, want e1", entry.MessageID)
	}
	if len(entry.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(entry.Parts))
	}
	if tc := entry.Parts[0].Content.(UnifiedTextContent); tc.Text != "hello there" {
		t.Errorf("text = %q", tc.Text)
	}
	if entry.Timestamp.IsZero() {
		t.Error("timestamp should parse from the entry timestamp field")
	}
}

func TestPiNormalizer_NormalizeLine_AssistantUsageAndCost(t *testing.T) {
	n := NewPiNormalizer()
	line := `{"type":"message","id":"e2","parentId":"e1","timestamp":"2026-07-01T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"hi"}],"model":"claude-sonnet-4-5","usage":{"input":100,"output":20,"cacheRead":400,"cacheWrite":30,"reasoning":5,"totalTokens":550,"cost":{"input":0.0003,"output":0.0003,"cacheRead":0.00012,"cacheWrite":0.0001125,"total":0.0008625}},"stopReason":"stop","timestamp":1782900002000}}`

	entry, err := n.NormalizeLine([]byte(line))
	if err != nil {
		t.Fatalf("NormalizeLine: %v", err)
	}
	if entry == nil || entry.Tokens == nil {
		t.Fatalf("expected tokens, got %+v", entry)
	}
	tok := entry.Tokens
	// pi's usage buckets are already split (input excludes cacheRead/cacheWrite,
	// unlike codex's aggregate input_tokens) — mapped 1:1.
	if tok.Input != 100 || tok.Output != 20 || tok.CacheRead != 400 || tok.CacheWrite != 30 || tok.Reasoning != 5 {
		t.Errorf("tokens = %+v", tok)
	}
	// The native per-message dollar cost is authoritative; it must survive so
	// no pricing-table lookup is needed for pi.
	if tok.Cost != 0.0008625 {
		t.Errorf("Cost = %v, want 0.0008625", tok.Cost)
	}
}

func TestPiNormalizer_NormalizeLine_NonConversationEntriesSkipped(t *testing.T) {
	n := NewPiNormalizer()
	for _, line := range []string{
		`{"type":"session","version":3,"id":"s1","timestamp":"2026-07-01T10:00:00.000Z","cwd":"/x"}`,
		`{"type":"model_change","id":"m1","parentId":null,"timestamp":"2026-07-01T10:00:00.000Z","provider":"anthropic","modelId":"claude-sonnet-4-5"}`,
		`{"type":"thinking_level_change","id":"t1","parentId":"m1","timestamp":"2026-07-01T10:00:00.000Z","thinkingLevel":"high"}`,
		`{"type":"label","id":"l1","parentId":"t1","timestamp":"2026-07-01T10:00:00.000Z","targetId":"m1","label":"x"}`,
		`{"type":"session_info","id":"si1","parentId":"l1","timestamp":"2026-07-01T10:00:00.000Z","name":"my session"}`,
		`{"type":"custom","id":"c1","parentId":"si1","timestamp":"2026-07-01T10:00:00.000Z","customType":"grove","data":{}}`,
		`{"type":"custom_message","id":"cm1","parentId":"c1","timestamp":"2026-07-01T10:00:00.000Z","customType":"grove","content":"hidden","display":false}`,
	} {
		entry, err := n.NormalizeLine([]byte(line))
		if err != nil {
			t.Fatalf("NormalizeLine(%s): %v", line, err)
		}
		if entry != nil {
			t.Errorf("expected nil entry for line %s, got %+v", line, entry)
		}
	}
}

// TestNormalizePiFile_BranchedFixture is the core linearization test: the
// active path is leaf (last entry) -> parentId chain -> root, so the
// abandoned branch entry must not appear and order must be conversation order.
func TestNormalizePiFile_BranchedFixture(t *testing.T) {
	f, err := os.Open(filepath.FromSlash(piFixturePath))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	entries, err := NormalizePiFile(f)
	if err != nil {
		t.Fatalf("NormalizePiFile: %v", err)
	}

	// Expected active path: aa000001 (user), aa000002 (assistant
	// thinking+text+toolCall), aa000003 (toolResult), bb000004 (user retry),
	// bb000005 (assistant). The label leaf contributes no entry; aa000004
	// (abandoned branch) is dropped.
	wantIDs := []string{"aa000001", "aa000002", "aa000003", "bb000004", "bb000005"}
	if len(entries) != len(wantIDs) {
		t.Fatalf("entries = %d, want %d: %+v", len(entries), len(wantIDs), entries)
	}
	for i, want := range wantIDs {
		if entries[i].MessageID != want {
			t.Errorf("entries[%d].MessageID = %q, want %q", i, entries[i].MessageID, want)
		}
	}

	for _, e := range entries {
		if e.Provider != "pi" {
			t.Errorf("Provider = %q, want pi", e.Provider)
		}
		for _, p := range e.Parts {
			if tc, ok := p.Content.(UnifiedTextContent); ok && strings.Contains(tc.Text, "ABANDONED") {
				t.Error("abandoned branch content leaked into the linearized transcript")
			}
		}
	}

	// Assistant turn shape: reasoning + text + tool_call, with usage+cost.
	a2 := entries[1]
	if a2.Role != "assistant" || len(a2.Parts) != 3 {
		t.Fatalf("aa000002 shape wrong: %+v", a2)
	}
	if a2.Parts[0].Type != "reasoning" || a2.Parts[1].Type != "text" || a2.Parts[2].Type != "tool_call" {
		t.Errorf("aa000002 part types = %s/%s/%s", a2.Parts[0].Type, a2.Parts[1].Type, a2.Parts[2].Type)
	}
	call := a2.Parts[2].Content.(UnifiedToolCall)
	if call.ID != "tc-1" || call.Name != "bash" || call.Input["command"] != "ls -la" {
		t.Errorf("tool call = %+v", call)
	}
	if a2.Tokens == nil || a2.Tokens.Input != 1200 || a2.Tokens.CacheRead != 1000 || a2.Tokens.Cost != 0.0062375 {
		t.Errorf("aa000002 tokens = %+v", a2.Tokens)
	}

	// Tool result binds back to the call id.
	res := entries[2].Parts[0].Content.(UnifiedToolResult)
	if res.ToolCallID != "tc-1" || res.Output != "main.go\nutil.go" || res.IsError {
		t.Errorf("tool result = %+v", res)
	}

	// Final assistant message on the retry branch carries its own cost.
	b5 := entries[4]
	if b5.Tokens == nil || b5.Tokens.Cost != 0.0105 {
		t.Errorf("bb000005 tokens = %+v", b5.Tokens)
	}
}

func TestNormalizePiFile_EmptyAndHeaderOnly(t *testing.T) {
	entries, err := NormalizePiFile(strings.NewReader(""))
	if err != nil || entries != nil {
		t.Errorf("empty file: entries=%v err=%v", entries, err)
	}
	entries, err = NormalizePiFile(strings.NewReader(`{"type":"session","version":3,"id":"s1","timestamp":"t","cwd":"/x"}` + "\n"))
	if err != nil || len(entries) != 0 {
		t.Errorf("header-only file: entries=%v err=%v", entries, err)
	}
}
