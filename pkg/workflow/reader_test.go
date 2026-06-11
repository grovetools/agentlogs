package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

const sampleScript = `export const meta = {
  name: 'release-survey',
  description: 'Deep survey for release readiness',
  phases: [
    { title: 'Survey', detail: 'parallel deep-dives' },
    { title: 'Critique', detail: 'completeness critic' },
  ],
}

phase('Survey')
`

// buildRun creates a synthetic wf_* run dir plus a scripts dir holding the
// persisted script for the run.
func buildRun(t *testing.T) (runDir, scriptsDir string) {
	t.Helper()
	base := t.TempDir()
	runDir = filepath.Join(base, "subagents", "workflows", "wf_test-run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptsDir = filepath.Join(base, "workflows", "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "release-survey-wf_test-run.js"), []byte(sampleScript), 0o600); err != nil {
		t.Fatal(err)
	}
	return runDir, scriptsDir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadWorkflowRun(t *testing.T) {
	runDir, scriptsDir := buildRun(t)

	// >1MB result line: would silently truncate with bufio.Scanner defaults.
	bigText := strings.Repeat("x", 2*1024*1024)
	bigResult := fmt.Sprintf(`{"summary":"done","detail":"%s"}`, bigText)
	journal := strings.Join([]string{
		`{"type":"started","key":"v2:aaa","agentId":"agent-one"}`,
		`{"type":"started","key":"v2:bbb","agentId":"agent-two"}`,
		`not json at all {{{`,
		`{"type":"mystery","key":"v2:ccc","agentId":"agent-one"}`,
		fmt.Sprintf(`{"type":"result","key":"v2:aaa","agentId":"agent-one","result":%s}`, bigResult),
	}, "\n") + "\n"
	writeFile(t, filepath.Join(runDir, "journal.jsonl"), journal)

	writeFile(t, filepath.Join(runDir, "agent-agent-one.jsonl"),
		`{"type":"user","isSidechain":true,"message":{"role":"user","content":"survey the nb module"}}`+"\n"+
			`garbage line`+"\n"+
			`{"type":"assistant","isSidechain":true,"agentId":"agent-one","message":{"id":"msg_1","content":[{"type":"text","text":"on it"}]}}`+"\n")

	run, err := ReadWorkflowRun(runDir, []string{filepath.Join(t.TempDir(), "missing"), scriptsDir})
	if err != nil {
		t.Fatal(err)
	}

	if run.RunID != "wf_test-run" {
		t.Errorf("RunID = %q", run.RunID)
	}
	if run.Meta == nil || run.Meta.Name != "release-survey" || len(run.Meta.Phases) != 2 {
		t.Fatalf("Meta = %+v", run.Meta)
	}
	if len(run.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d: %v", len(run.Agents), run.Agents)
	}

	one := run.Agents["agent-one"]
	if one == nil || !one.Started {
		t.Fatalf("agent-one = %+v", one)
	}
	// Result preserved structurally and untruncated.
	var res struct {
		Summary string `json:"summary"`
		Detail  string `json:"detail"`
	}
	if err := json.Unmarshal(one.Result, &res); err != nil {
		t.Fatalf("result not structurally valid JSON: %v", err)
	}
	if res.Summary != "done" || len(res.Detail) != len(bigText) {
		t.Errorf("result summary=%q detail len=%d (want %d)", res.Summary, len(res.Detail), len(bigText))
	}
	if len(one.Entries) != 2 {
		t.Fatalf("agent-one entries = %d: %+v", len(one.Entries), one.Entries)
	}
	if one.Entries[0].Role != "user" || one.Entries[0].AgentID != "agent-one" {
		t.Errorf("entry 0 = %+v", one.Entries[0])
	}
	if one.Entries[1].Role != "assistant" || one.Entries[1].MessageID != "msg_1" {
		t.Errorf("entry 1 = %+v", one.Entries[1])
	}

	two := run.Agents["agent-two"]
	if two == nil || !two.Started || two.Result != nil || len(two.Entries) != 0 {
		t.Errorf("agent-two = %+v", two)
	}
}

func TestReadWorkflowRun_MissingScriptAndJournal(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "wf_bare")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Transcript exists but no journal "started" event yet.
	writeFile(t, filepath.Join(runDir, "agent-late.jsonl"),
		`{"type":"user","message":{"role":"user","content":"hello"}}`+"\n")

	run, err := ReadWorkflowRun(runDir, []string{filepath.Join(runDir, "nope")})
	if err != nil {
		t.Fatal(err)
	}
	if run.Meta != nil {
		t.Errorf("Meta = %+v, want nil", run.Meta)
	}
	late := run.Agents["late"]
	if late == nil || late.Started || len(late.Entries) != 1 {
		t.Fatalf("late = %+v", late)
	}
	if late.Entries[0].AgentID != "late" {
		t.Errorf("entry AgentID = %q", late.Entries[0].AgentID)
	}
}

func TestReadWorkflowRun_MissingDir(t *testing.T) {
	if _, err := ReadWorkflowRun(filepath.Join(t.TempDir(), "wf_gone"), nil); err == nil {
		t.Fatal("expected error for missing run dir")
	}
}

func TestReadWorkflowRun_BufferedToolCallFlush(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "wf_flush")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Assistant entry with a tool call and no following tool_result: the
	// ClaudeNormalizer buffers it, so it must come out of Flush().
	writeFile(t, filepath.Join(runDir, "agent-a.jsonl"),
		`{"type":"assistant","message":{"id":"msg_t","content":[{"type":"tool_use","id":"tu_1","name":"Bash","input":{"command":"ls"}}]}}`+"\n")

	run, err := ReadWorkflowRun(runDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := run.Agents["a"]
	if a == nil || len(a.Entries) != 1 {
		t.Fatalf("agent a = %+v", a)
	}
	if a.Entries[0].AgentID != "a" {
		t.Errorf("AgentID = %q", a.Entries[0].AgentID)
	}
	hasToolCall := false
	for _, part := range a.Entries[0].Parts {
		if part.Type == "tool_call" {
			if tc, ok := part.Content.(transcript.UnifiedToolCall); ok && tc.Name == "Bash" {
				hasToolCall = true
			}
		}
	}
	if !hasToolCall {
		t.Errorf("expected buffered Bash tool_call, got parts %+v", a.Entries[0].Parts)
	}
}
