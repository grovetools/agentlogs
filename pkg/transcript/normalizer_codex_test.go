package transcript

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"
)

// codexFixturePath is a real-shaped codex rollout transcript in the nested
// ~/.codex/sessions/YYYY/MM/DD/ layout (see codex-rs/rollout/src/recorder.rs).
const codexFixturePath = "testdata/codex/sessions/2026/07/01/rollout-2026-07-01T10-00-00-5973b6c0-94b8-487b-a530-2aeb6098ae0e.jsonl"

func TestCodexNormalizer_FunctionCallPreservesFullArguments(t *testing.T) {
	n := NewCodexNormalizer()
	line := `{"timestamp":"2026-07-01T10:00:03.000Z","type":"response_item","payload":{"type":"function_call","name":"shell","arguments":"{\"command\":[\"bash\",\"-lc\",\"ls *.go\"],\"workdir\":\"/tmp/w\",\"timeout_ms\":120000}","call_id":"call_1"}}`

	entry, err := n.NormalizeLine([]byte(line))
	if err != nil {
		t.Fatalf("NormalizeLine: %v", err)
	}
	if entry == nil || len(entry.Parts) != 1 {
		t.Fatalf("expected one part, got %+v", entry)
	}
	tc, ok := entry.Parts[0].Content.(UnifiedToolCall)
	if !ok {
		t.Fatalf("part content type %T, want UnifiedToolCall", entry.Parts[0].Content)
	}
	if tc.ID != "call_1" || tc.Name != "shell" {
		t.Errorf("ID/Name = %q/%q, want call_1/shell", tc.ID, tc.Name)
	}
	// Full arguments must survive, not just a scraped command string.
	if _, ok := tc.Input["workdir"]; !ok {
		t.Error("workdir dropped from tool call input")
	}
	if _, ok := tc.Input["timeout_ms"]; !ok {
		t.Error("timeout_ms dropped from tool call input")
	}
	cmdArr, ok := tc.Input["command"].([]interface{})
	if !ok || len(cmdArr) != 3 {
		t.Fatalf("command should stay an argv array, got %#v", tc.Input["command"])
	}
	if cmdArr[2] != "ls *.go" {
		t.Errorf("command[2] = %v, want 'ls *.go'", cmdArr[2])
	}
}

func TestCodexNormalizer_NonShellFunctionCallKeepsInput(t *testing.T) {
	n := NewCodexNormalizer()
	line := `{"timestamp":"2026-07-01T10:00:05.000Z","type":"response_item","payload":{"type":"function_call","name":"update_plan","arguments":"{\"plan\":[{\"step\":\"a\",\"status\":\"completed\"}],\"explanation\":\"done\"}","call_id":"call_2"}}`

	entry, err := n.NormalizeLine([]byte(line))
	if err != nil {
		t.Fatalf("NormalizeLine: %v", err)
	}
	tc := entry.Parts[0].Content.(UnifiedToolCall)
	if tc.Name != "update_plan" {
		t.Errorf("Name = %q, want update_plan", tc.Name)
	}
	if _, ok := tc.Input["plan"]; !ok {
		t.Error("non-shell tool input dropped (plan key missing)")
	}
	if tc.Input["explanation"] != "done" {
		t.Errorf("explanation = %v, want done", tc.Input["explanation"])
	}
}

func TestCodexNormalizer_MalformedArgumentsKeptRaw(t *testing.T) {
	n := NewCodexNormalizer()
	line := `{"type":"response_item","payload":{"type":"function_call","name":"shell","arguments":"not-json","call_id":"call_3"}}`

	entry, err := n.NormalizeLine([]byte(line))
	if err != nil {
		t.Fatalf("NormalizeLine: %v", err)
	}
	tc := entry.Parts[0].Content.(UnifiedToolCall)
	if tc.Input["arguments"] != "not-json" {
		t.Errorf("raw arguments not preserved: %#v", tc.Input)
	}
}

func TestCodexNormalizer_TokenCount(t *testing.T) {
	n := NewCodexNormalizer()
	line := `{"timestamp":"2026-07-01T10:00:08.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1200,"cached_input_tokens":1000,"output_tokens":150,"reasoning_output_tokens":40,"total_tokens":1350},"last_token_usage":{"input_tokens":1200,"cached_input_tokens":1000,"output_tokens":150,"reasoning_output_tokens":40,"total_tokens":1350},"model_context_window":272000},"rate_limits":null}}`

	entry, err := n.NormalizeLine([]byte(line))
	if err != nil {
		t.Fatalf("NormalizeLine: %v", err)
	}
	if entry == nil {
		t.Fatal("token_count event should produce an entry")
	}
	if entry.Tokens == nil {
		t.Fatal("Tokens not captured from token_count event")
	}
	// input_tokens includes cached_input_tokens; UnifiedTokens splits them.
	if entry.Tokens.Input != 200 {
		t.Errorf("Input = %d, want 200 (1200 input - 1000 cached)", entry.Tokens.Input)
	}
	if entry.Tokens.CacheRead != 1000 {
		t.Errorf("CacheRead = %d, want 1000", entry.Tokens.CacheRead)
	}
	if entry.Tokens.Output != 150 {
		t.Errorf("Output = %d, want 150", entry.Tokens.Output)
	}
	if entry.Tokens.Reasoning != 40 {
		t.Errorf("Reasoning = %d, want 40", entry.Tokens.Reasoning)
	}
	if len(entry.Parts) != 0 {
		t.Errorf("token_count entry should carry no parts, got %d", len(entry.Parts))
	}
}

func TestCodexNormalizer_TokenCountLegacyFlatShape(t *testing.T) {
	n := NewCodexNormalizer()
	// Older codex serialized TokenUsage fields directly on the payload.
	line := `{"type":"event_msg","payload":{"type":"token_count","input_tokens":500,"cached_input_tokens":100,"output_tokens":50,"reasoning_output_tokens":10,"total_tokens":550}}`

	entry, err := n.NormalizeLine([]byte(line))
	if err != nil {
		t.Fatalf("NormalizeLine: %v", err)
	}
	if entry == nil || entry.Tokens == nil {
		t.Fatal("legacy flat token_count not captured")
	}
	if entry.Tokens.Input != 400 || entry.Tokens.CacheRead != 100 || entry.Tokens.Output != 50 {
		t.Errorf("legacy tokens = %+v, want Input 400 / CacheRead 100 / Output 50", entry.Tokens)
	}
}

func TestCodexNormalizer_TokenCountWithoutInfoSkipped(t *testing.T) {
	n := NewCodexNormalizer()
	// Rate-limit-only updates carry info:null and no usage — skip them.
	line := `{"type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"primary":{"used_percent":10}}}}`

	entry, err := n.NormalizeLine([]byte(line))
	if err != nil {
		t.Fatalf("NormalizeLine: %v", err)
	}
	if entry != nil {
		t.Errorf("expected nil entry for usage-less token_count, got %+v", entry)
	}
}

// TestCodexNormalizer_Fixture walks a full rollout transcript in codex's real
// nested-dir layout through the normalizer and checks the aggregate shape.
func TestCodexNormalizer_Fixture(t *testing.T) {
	f, err := os.Open(filepath.FromSlash(codexFixturePath))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	n := NewCodexNormalizer()
	var entries []*UnifiedEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		entry, err := n.NormalizeLine(scanner.Bytes())
		if err != nil {
			t.Fatalf("NormalizeLine: %v", err)
		}
		if entry != nil {
			entries = append(entries, entry)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	var users, reasonings, messages, toolCalls, toolResults, tokenEntries int
	for _, e := range entries {
		if e.Provider != "codex" {
			t.Errorf("Provider = %q, want codex", e.Provider)
		}
		if e.Tokens != nil {
			tokenEntries++
		}
		if e.Role == "user" && len(e.Parts) > 0 && e.Parts[0].Type == "text" {
			users++
		}
		for _, p := range e.Parts {
			switch p.Type {
			case "reasoning":
				reasonings++
			case "text":
				if e.Role == "assistant" {
					messages++
				}
			case "tool_call":
				toolCalls++
			case "tool_result":
				toolResults++
			}
		}
	}

	if users != 2 {
		t.Errorf("user entries = %d, want 2", users)
	}
	if reasonings != 1 {
		t.Errorf("reasoning parts = %d, want 1", reasonings)
	}
	if messages != 2 {
		t.Errorf("assistant text parts = %d, want 2", messages)
	}
	if toolCalls != 2 {
		t.Errorf("tool calls = %d, want 2", toolCalls)
	}
	if toolResults != 2 {
		t.Errorf("tool results = %d, want 2", toolResults)
	}
	// Two usage-bearing token_count events; the info:null one is skipped.
	if tokenEntries != 2 {
		t.Errorf("token-bearing entries = %d, want 2", tokenEntries)
	}
}

func TestParseCodexTokenCountLine_NonTokenLines(t *testing.T) {
	for _, line := range []string{
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[]}}`,
		`{"type":"event_msg","payload":{"type":"agent_message","message":"hi"}}`,
		`not json`,
	} {
		if _, ok := ParseCodexTokenCountLine([]byte(line)); ok {
			t.Errorf("line unexpectedly parsed as token_count: %s", line)
		}
	}
}
